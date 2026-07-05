# Demo quiz asset credits

The demo seed (`make seed-dev-demo`, or `go run ./cmd/seed-dev/ -seed=demo`)
restores every quiz archive under `dev/fixtures/demo/`. All bundled assets are in
the public domain; no attribution is legally required, so this file documents
provenance as a courtesy.

# Composers of Classical Music: Sights & Sounds (`demo-quiz.zip`)

Audio recordings from [Musopen](https://musopen.org); portraits from
[Wikimedia Commons](https://commons.wikimedia.org).

The file names below are the entries inside `demo-quiz.zip` (`media/<id>.<ext>`).
The media IDs are reassigned on import, so a seeded database will number them
differently.

## Audio (Musopen, public domain recordings)

| Archive file | Work | Composer |
| --- | --- | --- |
| `media/3.mp3` | Toccata and Fugue in D minor, BWV 565 (organ) | Johann Sebastian Bach |
| `media/4.mp3` | Nocturne in E-flat major, Op. 9 No. 2 | Frederic Chopin |
| `media/5.mp3` | Piano Sonata No. 11 in A major, K. 331, III. Alla Turca ("Turkish March") | Wolfgang Amadeus Mozart |
| `media/6.mp3` | Ave Maria, D. 839 | Franz Schubert |

## Portraits (Wikimedia Commons, public domain)

| Archive file | Subject |
| --- | --- |
| `media/7.jpg` | Johann Sebastian Bach |
| `media/8.jpg` | Franz Schubert |
| `media/9.jpg` | Ludwig van Beethoven |
| `media/10.jpg` | Claude Debussy (Nadar studio photograph) |
| `media/11.jpg` | Sergei Rachmaninoff |

# Name That Critter (`name-that-critter.zip`)

Animal sound clips (`media/<id>.mp3`) sourced from the
[Partners in Rhyme public-domain sound library](https://www.partnersinrhyme.com/soundfx/PUBLIC-DOMAIN-SOUNDS/animal1.shtml).
All clips are in the public domain.

# Free as in Public Domain (`free-as-in-public-domain.zip`)

A text-only quiz; the archive bundles no media assets.
