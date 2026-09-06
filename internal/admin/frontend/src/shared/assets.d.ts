// esbuild's `.png: "dataurl"` loader (build.mjs) turns an import like this
// into a `data:image/png;base64,...` string at bundle time — declare the
// module shape so tsc accepts the import (esbuild itself needs no types).
declare module "*.png" {
  const src: string;
  export default src;
}
