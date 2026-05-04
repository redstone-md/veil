// @veil/node — Node.js native addon entry shim.
//
// napi-rs would normally generate this file from `napi build`, but
// we ship a hand-written variant to keep the cross-platform binary
// resolution explicit and so consumers can read it without invoking
// the build pipeline.
//
// The native .node file is named after the host triple by `napi
// build --platform`. We probe each known suffix in order and
// throw a helpful error if none is present.

const { existsSync } = require("node:fs");
const { join } = require("node:path");
const { platform, arch } = process;

function loadNative() {
  const triples = candidateTriples(platform, arch);
  for (const t of triples) {
    const p = join(__dirname, `veil.${t}.node`);
    if (existsSync(p)) return require(p);
  }
  // Fallback: a debug build dropped in by `napi build` without
  // --platform names the file just `veil.node`.
  const generic = join(__dirname, "veil.node");
  if (existsSync(generic)) return require(generic);
  throw new Error(
    `@veil/node: could not find a native binary for ${platform}-${arch}. ` +
      `Looked for: ${triples.map((t) => `veil.${t}.node`).join(", ")}, veil.node. ` +
      `Build it with \`napi build --platform --release\` or install a prebuilt @veil/node-${platform}-${arch} package.`
  );
}

function candidateTriples(plat, ar) {
  switch (`${plat}-${ar}`) {
    case "win32-x64":   return ["win32-x64-msvc"];
    case "win32-arm64": return ["win32-arm64-msvc"];
    case "linux-x64":   return ["linux-x64-gnu", "linux-x64-musl"];
    case "linux-arm64": return ["linux-arm64-gnu", "linux-arm64-musl"];
    case "darwin-x64":  return ["darwin-x64"];
    case "darwin-arm64":return ["darwin-arm64"];
    default:            return [];
  }
}

module.exports = loadNative();
