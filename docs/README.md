# docs

The project's website and the demo dataset behind it. Published to GitHub
Pages by `.github/workflows/pages.yml`, which uploads this folder as it is —
there is no build step and no toolchain here, just files.

```
index.html          the whole site
assets/site.css     palette and type, copied from the app's globals.css
assets/site.js      the tour, the walkthrough, the architecture map
assets/charts.js    a plain-JS port of the app's SVG chart helpers
assets/shots/       screenshots, captured by desktop/scripts/capture-docs.mjs
assets/fonts/       Instrument Sans and Newsreader, latin subsets (SIL OFL)
assets/data/        charts.json, the series the site's charts draw
demo/               the demo dataset and the script that generates it
```

## Previewing

```sh
python3 -m http.server 8000 --directory docs
```

Then open <http://127.0.0.1:8000>. It has to be served: the page loads its
JavaScript as ES modules, which browsers refuse from a `file://` origin.

## Changing it

The prose, the layout and the tour captions are in `index.html` and
`assets/site.js`; edit them and reload. Nothing needs rebuilding.

The screenshots and `assets/data/charts.json` are generated. To refresh them
— after a UI change, or to move the demo year forward — see
[`demo/README.md`](demo/README.md). That script also rewrites the block of
JSON near the bottom of `index.html`, between the `demo-data` markers, so
that file will show up in the diff.

## Publishing

Pushing to `main` with changes under `docs/` deploys the site. The one
manual step, needed once per repository: **Settings → Pages → Source** must
be set to **GitHub Actions**, not "Deploy from a branch". The workflow
cannot set that itself.

The site is served from a subpath (`/money-insighter/`), so every link and
`src` in `index.html` is relative. An absolute `/assets/…` would break.
