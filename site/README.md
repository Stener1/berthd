# site

Shipyard's landing page: static HTML and CSS, no build step. Open
`index.html`, or serve the folder as it is.

- `index.html`, `styles.css`: the page. Light and dark follow the system.
- `assets/harbour.js` and `assets/harbour/`: the hero's harbour, the same
  painting the app's Labs home shows (day, and night in dark mode), drawn
  as the app's DitherBand draws it: an ordered dither of its lightness on a
  canvas at one dot per 2 CSS px, dissolving dot by dot into the page. It
  renders once per size; without the script the plain painting shows. The
  lighthouse's beam is a CSS wedge of halftone dots from the lamp, sweeping
  by transform only (still for reduced motion, paused when hidden).
- "Close the laptop" in the header puts the page to night by hand
  (`data-theme` on `<html>`): the harbour redraws at night.
- At night (dark mode, or the laptop closed) the harbour comes alive:
  `assets/hero-loop.js` plays the film's seamless loop
  (`assets/film/hero-loop.mp4`/`.webm`, 7.8 s, ~1.2 MB) in the band, from its
  first frame (`hero-loop-poster.webp`), dissolving into the page through
  the same 2 px dots. Its frames are drawn on a canvas, so it can never be the
  page's largest paint. By day the dithered day painting stays. Nothing of it
  loads before the hero has painted, and nothing at all for reduced motion or
  Save-Data.
- "Watch the film" above the headline opens the launch film in a `<dialog>`
  lightbox (native controls; Esc, the close button or the backdrop closes it;
  `#film` opens it). Nothing of the film loads until it's opened. See "The
  film" below for where its files live.
- Below the hero, the film's story in six chapters (Start, Watch it work,
  Side by side, Make it yours, It keeps going, Install): each one of the
  app's line scenes with its one amber light (`assets/scenes.json` and
  `assets/scenes.css`, from `scripts/scenes.mjs`), the film's caption as its
  heading, and a short muted loop of the real app (`assets/loops/`, 0.2-0.7
  MB each, cut by `scripts/loops.sh` from the film's footage; posters by
  `scripts/posters.mjs`; the Compare tab, which the film predates, by
  `scripts/compare.mjs` from the app in mock mode). "Watch it work" (steps,
  questions, artifacts) and "Side by side" (tab groups, Compare) switch
  between loops, each handing on to the next until one is chosen. "It keeps going" is always
  night, and reuses the hero's loop. `assets/clips.js` gives each poster its
  src and its loop once the page has loaded and it's near, plays loops only
  in view, and leaves the posters for reduced motion.
- Everything below the hero (the scenes, the loops, their scripts) is asked
  for only after the page has loaded and the harbour has been drawn, so the
  hero's LCP is what it was without them.
- `assets/shots/`: screenshots from the live demo with Labs on, as WebPs
  per theme at 1x and 2x (720, 1280 and 2080 for the full-window ones), and
  a readable crop of each for phones (`<scene>-<theme>-phone-<width>.webp`;
  the conversation pane's is `pane-conversation-narrow`, taken at a
  phone's width). They load lazily. Home, conversation, dashboard and zen
  need the Vite demo; pane-terminal, pane-conversation(-narrow), attempts
  and review click their way in, so they also work against the built demo:
  `node site/scripts/capture.mjs --url http://127.0.0.1:1460/demo/ --only
  pane-terminal,attempts` with `site/` served on 1460.
- `review/`: the bounce page for the **Review in Shipyard** button on PRs
  (`/review?repo=O/N&pr=N[&sha=…][&as=…][&path=…]`, rewritten to
  `review/index.html` in `vercel.json`). It checks the parameters as strictly
  as the app's `review-link.ts`, opens the `berth://review?…` link rebuilt
  from them, and shows the PR, a link to it on GitHub and where to install.
  One inline script, allowed by hash in its CSP (the page's `<meta>` and
  `vercel.json`): change the script and `review/review.test.mjs` (run with
  the app's `pnpm test`) says the new hash.
- `badges/review.svg`: the button itself, one small SVG that reads on
  GitHub's light and dark themes.
- `demo/`: the live demo, built from `app/` (see below). Committed, so the
  site still has no build step.
- `assets/og-film.jpg`: the 1200×630 link preview for Open Graph and
  Twitter, cut from the film's poster (`assets/og.png`, the earlier one, is
  kept but no longer referenced); `assets/favicon.svg` and `assets/apple-touch-icon.png`.
- `assets/fonts/`: Inter and JetBrains Mono (SIL OFL, licences beside them).
- `scripts/capture.mjs`: retakes the screenshots.

## The live demo

"Try the live demo" opens `/demo/`: the real app, running in the browser on
invented fixtures (`app/src/lib/mock*.ts`). Nothing of it loads from the
page itself.

It is `pnpm -C app build:demo` (`vite build --mode demo`), which writes
`site/demo/`. Rebuild and commit it when the app changes:

```sh
pnpm -C app install       # once
pnpm -C app build:demo    # about 3.3 MB in 55 files; ~640 KB gzipped to open
```

Demo mode is always mock mode, follows the system's light or dark, starts
afresh on every load, and leaves out what needs a laptop agent or Tauri:
no system notifications, no plugins from `~/.berth/plugins`, and a box's
dev server is a page drawn in place. Its own code is in `app/src/demo/`:
the guide (open checkout-fix, answer the waiting agent, press ⌘K, and Reset
demo), the script (an agent on gpu finishes, Codex on qa-deck stops to ask
something, a review lands in the inbox) and the agents' terminals, which
answer. None of it is in the app's own build (`__BERTH_DEMO__` is false
there). It loads nothing from anywhere but its own folder.

## The screenshots

```sh
npm install --prefix site/scripts --no-save playwright-core   # once
node site/scripts/capture.mjs
```

It builds the built-in plugins, starts Vite on `app/` in demo mode (port
1456), opens the demo with `?shots=1&labs=1` (no guide, badge or script;
Labs on) in headless Google Chrome at 2x in light and dark, stages each
scene, writes the WebPs into `assets/shots/`, and stops Vite. A console
error in the demo makes it exit non-zero. Also `--only home,zen`, `--theme
light`, `--png /tmp/shots` (keep the 2x PNGs), `--phone-only` (re-cut the
phone crops, no app needed), `--url URL` (a demo already served; no
Vite), `--port N`, `--quality 0.8`, `--skip-plugins`,
and the environment variables `PLAYWRIGHT_CORE` and `CHROME_CHANNEL`.

## The film

The lightbox plays `film/berth-launch-1080p-av1.webm` (AV1 10-bit + Opus,
~36 MB) and falls back to `film/berth-launch-1080p.mp4` (H.264 High, 6 Mbps
+ AAC, faststart, ~58 MB), both 1920×1080 at 60 fps, 74.6 s. They are **not
in the repository**: `site/film/` is ignored. A 36-58 MB file doesn't belong
in git history (every clone, and the Go module zip, would carry it), and
serving it from the site's own deployment spends its bandwidth.

Where to put them, best first:

1. **Vercel Blob** on the site's own team: a CDN with range requests and
   the right `Content-Type`, no trackers, nothing else to sign up for.
   `vercel blob put film/berth-launch-1080p.mp4 …`, then point the two
   `<source>`s and the link's `href` in `index.html` at the blob URLs (or
   add a redirect from `/film/:file` to them in `vercel.json`).
2. A GitHub release asset: free, but served as an attachment
   (`application/octet-stream`, behind a redirect), which Safari won't
   stream in a `<video>`.
3. YouTube or Vimeo: the least work, but their player brings their
   cookies and scripts to a page that has none.

To preview the lightbox locally, put the two encodes in `site/film/`.
They're made from the ProRes master:

```sh
ffmpeg -i berth-launch-1080p60-prores.mov -i berth-launch-1080p60.mp4 -map 0:v -map 1:a \
  -c:v libx264 -preset slow -b:v 6M -maxrate 8M -bufsize 12M -profile:v high -pix_fmt yuv420p -g 120 \
  -c:a aac -b:a 160k -movflags +faststart film/berth-launch-1080p.mp4      # two-pass in practice
ffmpeg -i berth-launch-1080p60-prores.mov -i berth-launch-1080p60.mp4 -map 0:v -map 1:a \
  -c:v libsvtav1 -preset 5 -crf 32 -g 240 -pix_fmt yuv420p10le -c:a libopus -b:a 128k \
  film/berth-launch-1080p-av1.webm
```

## Rules

The page follows the brand board (`design/brand/index.html`) and the app:
Inter and JetBrains Mono, coss-ui's buttons, sentence case, amber only for
what needs you (the mark's dot). The harbour and the film's night are the only
pictures that aren't the app. Keep it light: about 250 KB to first paint,
nothing below the hero requested before the hero has painted, each loop
under 1 MB; no layout shift (every image and loop has its size); no
trackers.
