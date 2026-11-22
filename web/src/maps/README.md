# Map data

The `*.js` files here are **generated**. Do not edit them: change the dataset
or `web/scripts/build-maps.mjs` and regenerate. They are committed so that
building the dashboard needs neither network access nor GIS tooling, and each
is loaded on demand as its own chunk (`web/src/maps.js`).

Each module exports `{ id, credit, viewBox, regions: [{ id, name, d, cx, cy, small? }], other?, overlays? }`:
SVG path data in a 1000-unit-wide viewBox, one region per country or
province, with its centre for dots and tooltips.

## world.js

- **Source**: Natural Earth, 1:10m Cultural Vectors, *Admin 0 – Countries*,
  China point-of-view edition (`ne_10m_admin_0_countries_chn.zip`, version 5.1.1),
  <https://www.naturalearthdata.com/downloads/10m-cultural-vectors/>.
- **Terms**: public domain. "No permission is needed to use Natural Earth.
  Crediting the authors is unnecessary." (<https://www.naturalearthdata.com/about/terms-of-use/>)
- **Processing**: Antarctica dropped; features dissolved by country code, with
  Hong Kong and Macau merged into China (Taiwan already is in this edition);
  simplified to 1 %; Robinson projection centred on 150°E. Region ids are
  ISO 3166-1 alpha-2 codes, which is what `Geo.cc` holds.

```bash
cd web
node scripts/build-maps.mjs --world /path/to/ne_10m_admin_0_countries_chn.shp
```

## china.js (not generated yet)

- **Source**: 天地图 行政区划可视化, province level, GeoJSON,
  <https://cloudcenter.tianditu.gov.cn/administrativeDivision>. The page states
  审图号 GS（2024）0650号 and, as its terms, "该数据仅供地图可视化使用"; the
  dashboard uses it for exactly that. Keep the attribution line under the map
  and do not alter the boundaries.
- Downloading needs a 天地图 account and a slider check, so it is done by hand
  in a browser. Then:

```bash
cd web
node scripts/build-maps.mjs --china /path/to/downloaded.geojson
```

Until `china.js` exists the dashboard offers the world map only; the 中国 /
全球 switch appears by itself once the file is there.
