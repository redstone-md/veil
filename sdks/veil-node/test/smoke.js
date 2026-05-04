// @veil/node — smoke test.
//
// Loads the addon, asks for the library version (which does not
// require a running session), and prints it. Exits 0 on success.

const veil = require("../index.js");

const v = JSON.parse(veil.libraryVersion());
console.log("libveil:", v);
if (!v.version) {
  console.error("missing version field");
  process.exit(1);
}
console.log("ok");
