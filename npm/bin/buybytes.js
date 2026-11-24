#!/usr/bin/env node
// Runs the prebuilt buybytes binary for this Mac.
const { spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");

if (process.platform !== "darwin") {
  console.error("buybytes supports macOS for now. Linux and Windows are on the way.");
  process.exit(1);
}
const bin = path.join(__dirname, `buybytes-darwin-${process.arch === "arm64" ? "arm64" : "x64"}`);
try {
  fs.chmodSync(bin, 0o755);
} catch {}
const r = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (r.error) {
  console.error(`buybytes: ${r.error.message}`);
  process.exit(1);
}
process.exit(r.status === null ? 1 : r.status);
