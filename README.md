# Wikitrivia Classic

A fan project keeping the original gameplay of Wikitrivia alive.

[Tom Watson's original Wikitrivia](https://github.com/tom-james-watson/wikitrivia) was updated to v2 in 2026 and made many changes. Wikitrivia Classic will stay faithful to the 2022 gameplay, with minimal site updates and a refreshed dataset.

## Usage

### Prerequisites

```bash
npm install
```

### Development

```bash
npm run dev
```

Then visit http://localhost:3000/ to preview the website.

### Static build

To build a static version of the website to the `out` folder, run:

```bash
npm run build
```

Then run said build with:

```bash
npm start
```

### Docker

A `Dockerfile` is included that builds the static site and serves it with nginx:

```bash
docker build -t wikitrivia-classic .
docker run --rm -p 8080:80 wikitrivia-classic
```

## Rebuilding the card dataset

`public/items.json` is generated from a [Wikidata JSON dump](https://dumps.wikimedia.org/wikidatawiki/entities/) by `scripts/build-items.go`:

```bash
go run scripts/build-items.go
```

Plan for around 150 GB of free disk space for the dump. On a 32-thread server the cards are computed in about 10 minutes.

Configuration is via environment variables (`WIKIDATA_DUMP_PATH`, `OUTPUT_PATH`, `SITELINKS_THRESHOLD`, `HUMAN_DOB_CUTOFF`, `SKIP_DOWNLOAD`, …). See the comment at the top of the script for details.

## FAQ

### Where does the data come from?

All card data is sourced from [Wikidata](https://www.wikidata.org) under the [CC0 license](https://creativecommons.org/publicdomain/zero/1.0/).

## License & credits

- Original Wikitrivia © 2022 Thomas James Watson, released under the MIT license.
- Card data sourced from [Wikidata](https://www.wikidata.org) under the CC0 license.

Report site issues or reach out by contacting <wikitrivia@michaelcc.me>.
