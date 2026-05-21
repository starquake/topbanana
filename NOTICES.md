# Third-party notices

This file lists third-party assets bundled in the topbanana source tree and
binary distribution. Runtime CDN dependencies (Google Fonts) are not
redistributed and are not listed here.

## Lucide icons — ISC License

The banana icon used in the favicon, the brand mark in the navbar and
player client, and the Open Graph share image is the `banana` icon from
the [Lucide icon library](https://lucide.dev). Paths appear inline in the
admin, auth, and player templates and as standalone SVGs at
`internal/web/static/banana.svg` and `internal/web/static/_og-image.svg`.
The rendered `internal/web/static/og-image.png` incorporates the same paths.

Lucide is published under the ISC License:

```
ISC License

Copyright (c) for portions of Lucide are held by Cole Bemis 2013-2022 as
part of Feather (MIT). All other copyright (c) for Lucide are held by
Lucide Contributors 2022.

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

## htmx — Zero-Clause BSD (0BSD)

A copy of htmx 2.0 is vendored at `internal/web/static/js/htmx.min.js`
(introduced in #223). htmx is published under the BSD Zero-Clause License:

```
Copyright (c) 2020, Big Sky Software

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

## anime.js — MIT License

A copy of anime.js 4.2.0 is vendored at
`internal/client/static/js/vendor/anime.umd.min.js` (introduced in #295,
previously loaded from cdn.jsdelivr.net). anime.js is published under
the MIT License:

```
The MIT License (MIT)

Copyright (c) 2019 Julian Garnier

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

## Alpine.js — MIT License

A copy of Alpine.js 3.15.12 is vendored at
`internal/client/static/js/vendor/alpine.min.js` (introduced in #295,
previously loaded from cdn.jsdelivr.net). Alpine.js is published under
the MIT License:

```
The MIT License (MIT)

Copyright (c) 2019-2024 Caleb Porzio and contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

## Tailwind CSS — MIT License

The utility CSS in `internal/web/static/css/app.css` is generated from
[Tailwind CSS v4](https://tailwindcss.com). The minified output retains
its own MIT attribution comment at the top of the file. Tailwind is
published under the MIT License.

## Go module dependencies

Runtime Go dependencies are listed in `go.mod`. Each module ships its own
LICENSE file inside the Go module cache; their terms are not restated here.
