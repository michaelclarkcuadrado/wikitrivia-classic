const fs = require("fs");
const zlib = require("zlib");
const path = require("path");

const src = path.join(__dirname, "..", "public", "items.json");
const dst = src + ".gz";

if (!fs.existsSync(src)) {
  console.error(`gzip-items: ${src} not found`);
  process.exit(1);
}

if (fs.existsSync(dst) && fs.statSync(dst).mtimeMs >= fs.statSync(src).mtimeMs) {
  process.exit(0);
}

const input = fs.readFileSync(src);
const output = zlib.gzipSync(input, { level: 9 });
fs.writeFileSync(dst, output);
console.log(
  `gzip-items: ${input.length} -> ${output.length} bytes (${(
    (output.length / input.length) * 100
  ).toFixed(1)}%)`
);
