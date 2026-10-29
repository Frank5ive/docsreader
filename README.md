# docsreader

A local book reader for EPUB and PDF, written in pure Go. It runs as a single
binary, serves its interface to your browser on `127.0.0.1`, and keeps your
library, reading positions and notes in a local SQLite file. No cgo, no
external services, no account.

## Features

- **Resume anywhere.** Your position is stored as a reflow-stable anchor
  (chapter, paragraph, character offset) rather than a page number, so it
  survives changes to font, size and window width. PDFs resume on the exact page
  and how far down it you were.
- **PDFs scroll continuously** with zoom (50-300%), drawing only the pages near
  the screen so large files stay fast.
- **Keyboard shortcuts** for reading, bookmarks and zoom; press `?` for the list.
- **Highlights, notes and bookmarks** in both EPUB and PDF, with Markdown export.
- **Full-text search** across your whole library or inside one book, with
  stemming ("learning" finds "learn"). PDF text is extracted in pure Go.
- **Reading stats:** time read, speed, streaks, a 30-day chart, time per book
  and an estimate of time left.
- **Comfortable reading:** themes (light, sepia, dark, or follow the system),
  fonts, size, line height and width, a focus mode, and toolbars that step aside
  while you scroll.
- **Library tools:** generated covers, tags with filtering, drag-and-drop import,
  backup and restore.

## Build and run

Requires Go 1.26 or newer. No C toolchain is needed on any platform.

```sh
make build                      # builds ./bin/docsreader
./bin/docsreader book.epub      # imports the file(s) and opens your browser
./bin/docsreader                # open the existing library
```

Flags:

| Flag | Meaning |
| --- | --- |
| `-data DIR` | data directory (default: `docsreader` under your user config dir) |
| `-port N` | listen port (default: random) |
| `-no-browser` | print the URL instead of opening a browser |
| `-restore FILE.zip` | restore a backup into the data directory and exit |

The server binds to `127.0.0.1` only. A random per-run token in the printed URL
authorises your browser; requests without it, or with a foreign `Host` header,
are rejected.

Release builds for all six targets (Linux, macOS, Windows on amd64 and arm64):

```sh
make release                    # writes ./dist/
```

## Backup and restore

The **Backup** button on the library page downloads one zip with the database and
every book file. To restore, stop the app and run:

```sh
./bin/docsreader -restore docsreader-backup-2026-10-29.zip
```

Existing data is moved aside to `*.before-restore` rather than overwritten.

## Architecture

Clean architecture; dependencies point inward.

```
cmd/docsreader          composition root: flags, wiring, server start
internal/domain         entities (Book, Anchor, Annotation, Session); stdlib only
internal/app            use cases and the ports they depend on
internal/adapters/
    format/epub         EPUB parsing, sanitising, table of contents
    format/pdf          PDF metadata and text extraction
    storage/sqlite      repositories, full-text index (modernc.org/sqlite)
    web                 JSON API and the embedded UI (HTML/CSS/JS, vendored pdf.js)
    platform            data directory, opening the browser per OS
```

## Tests

```sh
make test       # vet and unit tests
make e2e        # browser tests: drives the real UI in headless Chrome
```

`make e2e` needs Google Chrome or Chromium installed. It uses generated EPUB and
PDF files, so it does not depend on any documents of your own. To capture
screenshots of the main screens while working on the UI:

```sh
DR_SHOTS=/tmp/shots go test -tags e2e -run Screenshots ./internal/adapters/web
```

GitHub Actions (`.github/workflows/ci.yml`) runs vet and tests on Linux, Windows
and macOS, the browser tests, and a build of every release target.

## Limits

- DRM-protected books are not supported.
- PDF search results land on the page, not the exact word, and scanned PDFs
  have no text to search (there is no OCR).
- Highlights are located by paragraph and character offset, so they re-attach
  only if the book file is unchanged.
