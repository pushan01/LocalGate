import { copyFile, mkdir, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

import { build } from "esbuild";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const adminDir = path.resolve(scriptDir, "..");
const srcDir = path.join(adminDir, "src");
const distDir = path.join(adminDir, "dist");

await rm(distDir, { recursive: true, force: true });
await mkdir(distDir, { recursive: true });

await build({
  entryPoints: {
    app: path.join(srcDir, "app.js"),
    styles: path.join(srcDir, "styles.css"),
  },
  outdir: distDir,
  bundle: true,
  format: "esm",
  target: ["es2020"],
  charset: "utf8",
  legalComments: "none",
  sourcemap: false,
  minify: false,
  logLevel: "info",
});

await copyFile(path.join(srcDir, "index.html"), path.join(distDir, "index.html"));
