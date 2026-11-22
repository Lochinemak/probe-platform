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

## china.js

- **Source**: 天地图 行政区划可视化, province level, GeoJSON,
  <https://cloudcenter.tianditu.gov.cn/administrativeDivision> (data updated
  2026-10, downloaded 2026-11-23). The page states 审图号 GS（2024）0650号
  and, as its terms, "该数据仅供地图可视化使用"; the dashboard uses it for
  exactly that. The attribution line under the map (`credit`) must stay.
- Downloading needs a 天地图 account and a slider check, so it is done by hand
  in a browser: 选择地区 中国 → 下载区域 省 → GeoJSON → 下载数据.
- **Processing**: nothing is moved or left out. The 34 province-level
  polygons are simplified together (6 %, shared borders stay shared) and
  keyed by short name, which is what `Geo.province` holds. The dataset's
  境界线 line features, the South China Sea dashes among them, are drawn as
  given (`overlays`). Albers equal-area projection. The main frame ends at
  15.8°N; the sea south of it is shown in the usual inset (`inset`), which
  redraws the same shapes. Hainan keeps every ring (the South China Sea
  islands) and so does Taiwan (the Diaoyu Islands). The script refuses to
  write a map whose inset would cover any province.

```bash
cd web
node scripts/build-maps.mjs --china /path/to/中国_省.geojson
```

The 中国 / 全球 switch on the dashboard offers whichever of the two modules exist.
