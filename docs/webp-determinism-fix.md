# Fixing the deepteams/webp non-determinism

A field guide for landing the upstream changes that close out
[topbanana #976](https://github.com/starquake/topbanana/issues/976) and the
follow-up [#946](https://github.com/starquake/topbanana/issues/946). The
investigation behind this lives on the #976 issue comment dated 2026-06-15; this
doc is the actionable plan derived from it.

## Why this is needed

`internal/media/pipeline_test.go::TestProcessSHA256Deterministic` asserts that
`Process(reader)` produces byte-identical `Full` bytes for byte-identical
input. The sha256 over `Full` is load-bearing: it backs both the HTTP `ETag`
and the orphan-cleanup tooling that walks the media directory.

The assertion holds in production today only because every call to
`webp.Encode` and `webp.Decode` is serialised behind `webpMu` in
`internal/media/pipeline.go`. Without that mutex the deepteams/webp v1.2.4
codec produces non-deterministic output under concurrent encode load. The
mutex was added in [topbanana #945](https://github.com/starquake/topbanana/issues/945)
and is meant to come out once the upstream library is concurrency-safe
(tracked in #946). This document is the recipe for getting it concurrency-safe.

A diagnostic agent reproduced the failure mode locally with 32 worker
goroutines encoding image B in parallel while one goroutine repeatedly
encoded image A at matching dimensions (640x480). 170 of 200 image-A encodes
produced different `.Full` bytes or lengths than the canonical version. The
exact CI failure mode (two same-goroutine `Process(rawA)` calls disagreeing
on the bytes) is the same defect surfacing under coverage + race scheduling
pressure; it is harder to reproduce with a single-goroutine probe.

## What is broken upstream

deepteams/webp v1.2.4 has two independent bugs in this area.

### Bug 1: the gamma-table init race

`internal/dsp/yuv.go::InitGammaTables` guards its lazy table-build with a
plain `bool` instead of `sync.Once`. Concurrent first calls observe a partial
table; under `-race` the detector flags the unsynchronised read/write.

This is already covered by the upstream PR
<https://github.com/deepteams/webp/pull/9>. By the time `webp.Encode` returns
the tables are fully populated, so a later Encode call would not observe
partial state — the determinism bug below is a separate beast.

### Bug 2: pool reuse leaking state across goroutines

The hot encode path threads work through several `sync.Pool`s, each holding
reusable buffers larger than any single image needs:

- `internal/lossy/encode.go::encoderPool` — whole `VP8Encoder`, reset between
  uses by a partial `resetForReuse`.
- `internal/lossy/encode.go::importUVWorkerPool` — UV row workers used by the
  parallel encode fan-out.
- `internal/lossy/encode_parallel.go::parallelPool` — per-row state holding
  the shared top-context buffers (`topY`, `topU`, `topV`, `nz`).
- `internal/lossy/encode_syntax.go::boolWriterPool` — bit-stream writers.
- `encode.go::argbPool` — pixel staging area at the API boundary.
- Smaller pools under `internal/lossless/` and `internal/pool/`.

`encodeFrameParallel` spawns up to `runtime.GOMAXPROCS(0)` workers per
`Encode`, so under work-stealing or context switches a worker can pick up a
pool entry that was last used by a *different* image's encode. Several reuse
paths in `parallelPool` and `importUVWorker` slice the buffer down to the
current image's macroblock count
(`topY := ps.topY[:mbW*16]`, etc.) and assume the encode side will rewrite
exactly that range. When the read range is wider than the rewrite range — or
when the partial `resetForReuse` skips a field that ends up read before it is
re-set — the residual bytes from the prior image survive into the new
bitstream. The output is internally valid webp but differs bit-for-bit from a
fresh encode, and the variance is per-process / per-schedule.

The pools were added for throughput; the bug is that "reset for reuse" was
written as "reset what we know the next caller will overwrite" instead of
"reset everything observable downstream".

## The fix

Three upstream changes, in order. Each is independently mergeable.

### Change 1: land the gamma-table sync.Once (PR #9)

This is already on `main` as a draft pull request. Get it reviewed, rebased,
merged. After this, the `-race` tripping reported on
`webp.Encode` first calls goes away.

### Change 2: make every pool entry produce identical bytes regardless of history

Two acceptable shapes.

**Shape A — full zero on Put.** Add a single helper to each pool type that
zeroes every observable field, then call it just before
`pool.Put(entry)` in the encode hot path. Pros: simple, no API surface
change. Cons: forfeits some of the throughput win the pools were added for
(zeroing a 64 KB-ish buffer is not free).

**Shape B — opt-out via EncoderOptions.** Add a new `EncoderOptions` field
(suggested name: `DisablePoolReuse bool`). When set, the encode path
allocates fresh state every call and bypasses the pools entirely. Pros:
preserves throughput for callers that explicitly tolerate this state
leakage. Cons: callers that need determinism (everyone whose output is
hashed) have to opt in by name.

Recommend **shape A** for the upstream patch. Topbanana cannot rely on every
caller of `webp.Encode` in its dependency graph to set the opt-out flag, and
the encode cost dominated by the deflate stage anyway — zeroing the per-frame
scratch buffers is in the noise compared to the entropy coding.

The minimum set of fields each pool's `Put`-side zeroer must touch:

- `internal/lossy/encode.go::encoderPool`: extend `resetForReuse` to clear
  every `VP8Encoder` field that is read before being written by the next
  encode. The current implementation leaves some intermediate state in
  place; audit against the encode flow and zero or `:= nil` the rest.
- `internal/lossy/encode_parallel.go::parallelPool::getParallelState`:
  zero `topY`, `topU`, `topV`, `nz`, and any other slice the parallel rows
  read from before writing to. Slicing `topY[:mbW*16]` only constrains the
  *length* of the view; the underlying array still carries the prior
  encode's bytes past that prefix, and a stride mismatch then surfaces as a
  ghost edge in the new bitstream.
- `internal/lossy/encode.go::importUVWorkerPool`: clear the per-worker
  scratch UV planes on Put.
- `internal/lossy/encode_syntax.go::boolWriterPool`: zero the writer's
  internal range/value/bits-pending fields and rewind its byte cursor.
- `encode.go::argbPool`: zero the staging buffer on Put (or allocate fresh
  per call — this one is per-`Encode`, not per-row).

Each call site is a couple of lines. Run the workload reproducer below after
each pool's zeroer is added to see the failure rate drop.

### Change 3 (optional polish): document the contract

Add a sentence to `webp.Encode`'s godoc:

> Encode is safe for concurrent use from independent goroutines. The
> encoder's internal pools are zeroed on return, so the output bytes for a
> given input image depend only on the input and the `EncoderOptions`.

This is the contract topbanana's `internal/media/pipeline.go::Process`
relies on. Stating it explicitly upstream means we do not have to keep
re-verifying it every release.

## Workload reproducer

Save as a `_test.go` under the deepteams/webp module while iterating.

```go
package webp_test

import (
    "bytes"
    "image"
    "image/color"
    "io"
    "sync"
    "testing"

    "github.com/deepteams/webp"
)

func gradient(w, h, seed int) *image.RGBA {
    img := image.NewRGBA(image.Rect(0, 0, w, h))
    for y := 0; y < h; y++ {
        for x := 0; x < w; x++ {
            img.Set(x, y, color.RGBA{
                R: uint8((x + seed) & 0xFF),
                G: uint8((y + seed) & 0xFF),
                B: 128,
                A: 255,
            })
        }
    }

    return img
}

// TestEncode_ConcurrentDeterminism runs many parallel encodes of image B
// while one goroutine repeatedly encodes image A, then checks that every
// image A encode produced byte-identical output to the canonical version.
// On v1.2.4 this fails 100x+ out of 200 iterations.
func TestEncode_ConcurrentDeterminism(t *testing.T) {
    imgA := gradient(640, 480, 0)
    imgB := gradient(640, 480, 1)
    opts := &webp.EncoderOptions{Quality: 80}

    var canonical bytes.Buffer
    if err := webp.Encode(&canonical, imgA, opts); err != nil {
        t.Fatalf("canonical encode: %v", err)
    }

    const iterations = 200
    const noisemakers = 32
    fail := 0
    var mu sync.Mutex

    for i := 0; i < iterations; i++ {
        var wg sync.WaitGroup
        wg.Add(noisemakers)
        for j := 0; j < noisemakers; j++ {
            go func() {
                defer wg.Done()
                _ = webp.Encode(io.Discard, imgB, opts)
            }()
        }

        var buf bytes.Buffer
        if err := webp.Encode(&buf, imgA, opts); err != nil {
            t.Errorf("iter %d: encode A err = %v", i, err)
            wg.Wait()
            continue
        }
        wg.Wait()

        if !bytes.Equal(buf.Bytes(), canonical.Bytes()) {
            mu.Lock()
            fail++
            mu.Unlock()
        }
    }

    if fail != 0 {
        t.Fatalf("%d/%d iterations produced non-canonical bytes; pool state is leaking across encodes", fail, iterations)
    }
}
```

On a clean v1.2.4 this hits ~170/200 failures. With change 2 landed it must
report 0/200. Run under `-race` too; that combination is what CI hits.

## After the upstream chain lands

In topbanana:

1. Bump `github.com/deepteams/webp` in `go.mod` to the release containing
   both changes; run `go mod tidy`.
2. Remove `webpMu` and the corresponding lock pairs from
   `internal/media/pipeline.go::encodeWebP` and `decodeGuarded`.
3. Drop the matching `*ForTest` wrappers in `internal/media/export_test.go`
   and restore the direct `webp.Encode` / `webp.Decode` calls in
   `internal/media/pipeline_test.go`.
4. Keep `TestProcess_Concurrent` (it now proves the upstream fix alone is
   race-free).
5. Run `go test ./internal/media -count=20 -race -coverpkg=./...` to confirm
   `TestProcessSHA256Deterministic` no longer flakes.
6. Close #946 and #976 referencing the upstream release tag.

## Open questions for the upstream maintainer

- Are the pools held for a single-encode lifetime or shared across `Encode`
  calls? The investigation assumes the latter (the failure mode requires
  it). If the maintainer believes the former, the workload reproducer is the
  evidence.
- Is there a downstream test under `go test -race -count=N` that the
  upstream CI runs? If not, adding one with the reproducer above gates
  future regressions.
- Is there interest in a `DisablePoolReuse` flag as a transitional escape
  hatch? Topbanana would set it on every encode regardless.

## Sources

- The diagnostic agent's full reproduction and pool walkthrough lives on the
  #976 issue comment (2026-06-15).
- `internal/media/pipeline.go`, `internal/media/pipeline_test.go`,
  `internal/media/export_test.go` — the local code that depends on the
  upstream contract.
- Upstream PR <https://github.com/deepteams/webp/pull/9> — the gamma-table
  half of the fix.
