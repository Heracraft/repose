// Static server for dist/ on 0.0.0.0:4321.
// Usage: node serve.mjs [--watch] [--port N]
import { createServer } from "node:http";
import { readFile, stat } from "node:fs/promises";
import { watch } from "node:fs";
import { spawn } from "node:child_process";
import { dirname, join, normalize, extname, basename } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const dist = join(here, "dist");
const args = process.argv.slice(2);
const portArg = args.indexOf("--port");
const port = portArg >= 0 ? Number(args[portArg + 1]) : 4321;

const types = {
  ".html": "text/html; charset=utf-8", ".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8", ".json": "application/json", ".svg": "image/svg+xml",
  ".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".ico": "image/x-icon", ".woff2": "font/woff2",
};

const server = createServer(async (req, res) => {
  try {
    let p = decodeURIComponent(new URL(req.url, "http://x").pathname);
    if (p.endsWith("/")) p += "index.html";
    const file = normalize(join(dist, p));
    if (!file.startsWith(dist)) { res.writeHead(403).end("forbidden"); return; }
    let target = file;
    const s = await stat(target).catch(() => null);
    if (s && s.isDirectory()) target = join(target, "index.html");
    const body = await readFile(target);
    res.writeHead(200, { "content-type": types[extname(target)] || "application/octet-stream", "cache-control": "no-store" });
    res.end(req.method === "HEAD" ? undefined : body);
  } catch {
    res.writeHead(404, { "content-type": "text/plain; charset=utf-8" }).end("not found");
  }
});

server.listen(port, "0.0.0.0", () => console.log(`serving ${dist} on http://localhost:${port}`));

function rebuild(why) {
  const child = spawn(process.execPath, [join(here, "build.mjs")], { stdio: "inherit" });
  child.on("exit", (code) => console.log(code === 0 ? `rebuilt (${why})` : `build failed with exit ${code}`));
}

if (args.includes("--watch")) {
  const { proposals } = await import("./build.mjs");
  const files = new Set(proposals.map((p) => p.src));
  const dirs = new Set(proposals.map((p) => dirname(p.src)));
  let timer = null;
  const later = (why) => { clearTimeout(timer); timer = setTimeout(() => rebuild(why), 150); };
  // Watch directories so editors that replace files on save still trigger.
  for (const d of dirs) {
    watch(d, (_e, name) => { if (name && files.has(join(d, name.toString()))) later(name.toString()); });
  }
  for (const p of proposals) {
    const fd = join(here, p.key);
    try {
      watch(fd, (_e, name) => { if (name && /^index(-light)?\.html$/.test(name.toString())) later(`${p.key}/${name}`); });
    } catch {}
  }
  rebuild("start");
  console.log(`watching ${[...files].map((f) => basename(f)).join(", ")} and figures/`);
}
