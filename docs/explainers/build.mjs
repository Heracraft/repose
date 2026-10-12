// Builds each proposal's figure, then renders the proposals into dist/.
// Usage: node build.mjs (needs pnpm install for apps/web, whose marked it uses)
import { readFileSync, writeFileSync, mkdirSync, copyFileSync, existsSync } from "node:fs";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, "..", "..");
const { Marked } = createRequire(join(repo, "apps/web/package.json"))("marked");
const dist = join(here, "dist");

export const proposals = [
  { key: "price-level", n: 2, src: join(repo, "docs/proposals/2026-10-11-price-level.md") },
  { key: "pause-idle", n: 4, src: join(repo, "docs/proposals/2026-10-11-pause-idle-machines.md") },
];

const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
const strip = (html) => html.replace(/<[^>]+>/g, "");
const unesc = (s) => s.replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&#39;/g, "'");

function slugify(text) {
  return unesc(strip(text)).toLowerCase().replace(/[`'"]/g, "").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "section";
}

// Map links between the proposals onto the site's own pages.
const linkMap = new Map(proposals.map((p) => [p.src.split("/").pop(), `${p.key}.html`]));

function render(md) {
  const toc = [];
  const used = new Map();
  let title = "";
  const marked = new Marked({ gfm: true });
  marked.use({
    renderer: {
      heading({ tokens, depth }) {
        const inner = this.parser.parseInline(tokens);
        if (depth === 1) { title = inner; return ""; }
        let id = slugify(inner);
        const seen = used.get(id) || 0;
        used.set(id, seen + 1);
        if (seen) id = `${id}-${seen + 1}`;
        if (depth <= 3) toc.push({ depth, id, html: inner });
        return `<h${depth} id="${id}"><a class="anchor" href="#${id}" aria-label="Link to this section">#</a>${inner}</h${depth}>\n`;
      },
      table(token) {
        const cell = (c, tag) => {
          const align = c.align ? ` style="text-align:${c.align}"` : "";
          return `<${tag}${align}>${this.parser.parseInline(c.tokens)}</${tag}>`;
        };
        const head = `<tr>${token.header.map((c) => cell(c, "th")).join("")}</tr>`;
        const body = token.rows.map((r) => `<tr>${r.map((c) => cell(c, "td")).join("")}</tr>`).join("\n");
        return `<div class="table-wrap" tabindex="0"><table><thead>${head}</thead><tbody>${body}</tbody></table></div>\n`;
      },
      link({ href, title: t, tokens }) {
        const text = this.parser.parseInline(tokens);
        const file = (href || "").split("/").pop().split("#")[0];
        let target = href;
        if (linkMap.has(file)) target = linkMap.get(file) + (href.includes("#") ? href.slice(href.indexOf("#")) : "");
        const ext = /^https?:/.test(target) ? ' rel="noopener" target="_blank"' : "";
        return `<a href="${esc(target)}"${t ? ` title="${esc(t)}"` : ""}${ext}>${text}</a>`;
      },
    },
  });
  const html = marked.parse(md);
  // First paragraph after the title is the proposal's status and scope.
  const lead = (html.match(/<p>([\s\S]*?)<\/p>/) || [, ""])[1];
  const words = md.split(/\s+/).length;
  return { html, toc, title, lead, words };
}

const css = `
:root{
  --bg:#fbfbf9;--surface:#ffffff;--text:#26262a;--strong:#111114;--muted:#6b6b72;--line:#e3e3df;--line-strong:#cfcfca;
  --accent:#2f5bd3;--code-bg:#f1f1ee;--row:#f7f7f4;--mark:#fff3c4;
  --font:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif;
  --mono:ui-monospace,"SF Mono",Menlo,Consolas,monospace;
  color-scheme:light;
}
@media (prefers-color-scheme:dark){:root:not([data-theme="light"]){
  --bg:#0e0e0f;--surface:#151517;--text:#c9c9cd;--strong:#f0f0f2;--muted:#8a8a92;--line:#26262a;--line-strong:#36363b;
  --accent:#8fb0ff;--code-bg:#1c1c1f;--row:#131315;--mark:#3a3218;color-scheme:dark;
}}
:root[data-theme="dark"]{
  --bg:#0e0e0f;--surface:#151517;--text:#c9c9cd;--strong:#f0f0f2;--muted:#8a8a92;--line:#26262a;--line-strong:#36363b;
  --accent:#8fb0ff;--code-bg:#1c1c1f;--row:#131315;--mark:#3a3218;color-scheme:dark;
}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%;scroll-padding-top:16px}
body{margin:0;background:var(--bg);color:var(--text);font:17px/1.65 var(--font);overflow-x:hidden}
a{color:var(--accent);text-underline-offset:2px;text-decoration-thickness:1px}
code{font:.86em/1.4 var(--mono);background:var(--code-bg);padding:.12em .35em;border-radius:4px;overflow-wrap:anywhere}
pre{background:var(--code-bg);padding:12px 14px;border-radius:8px;overflow-x:auto}
pre code{background:none;padding:0;overflow-wrap:normal}
strong{color:var(--strong)}
em{font-style:italic}
.top{max-width:1240px;margin:0 auto;padding:20px 16px 0;font-size:14px;color:var(--muted);display:flex;gap:16px;flex-wrap:wrap}
.top a{color:var(--muted);text-decoration:none}.top a:hover{color:var(--text)}
.top .here{color:var(--strong)}
.layout{max-width:1240px;margin:0 auto;padding:0 16px 96px;display:grid;grid-template-columns:minmax(0,1fr);gap:48px}
@media (min-width:1100px){.layout{grid-template-columns:240px minmax(0,1fr)}}
.toc{display:none}
@media (min-width:1100px){.toc{display:block;position:sticky;top:0;align-self:start;max-height:100vh;overflow-y:auto;padding:40px 0 24px;font-size:13.5px;line-height:1.45}}
.toc h2{font-size:12px;letter-spacing:.06em;text-transform:uppercase;color:var(--muted);margin:0 0 10px;font-weight:600}
.toc ol{list-style:none;margin:0;padding:0}
.toc li{margin:0}
.toc a{display:block;color:var(--muted);text-decoration:none;padding:4px 0 4px 10px;border-left:2px solid transparent}
.toc a:hover{color:var(--text)}
.toc a.on{color:var(--strong);border-left-color:var(--accent)}
.toc .d3 a{padding-left:22px;font-size:12.5px}
main{min-width:0}
header.doc{max-width:72ch;padding-top:28px}
header.doc .kicker{font-size:13px;color:var(--muted);letter-spacing:.04em;text-transform:uppercase;margin:0 0 8px}
h1{font-size:clamp(28px,4vw,38px);line-height:1.15;color:var(--strong);margin:0 0 20px;letter-spacing:-.01em}
.figure{margin:8px 0 40px;max-width:880px}
.figure iframe{display:block;width:100%;height:560px;border:0;border-radius:12px;background:var(--surface)}
.figure p{font-size:13px;color:var(--muted);margin:8px 2px 0}
article{max-width:72ch}
article h2{font-size:24px;line-height:1.25;color:var(--strong);margin:56px 0 14px;padding-top:12px;border-top:1px solid var(--line)}
article h3{font-size:19px;color:var(--strong);margin:36px 0 10px}
article h4{font-size:16px;color:var(--strong);margin:28px 0 8px}
article h2,article h3,article h4{position:relative;scroll-margin-top:16px}
.anchor{position:absolute;left:-1.1em;width:1em;color:var(--muted);text-decoration:none;opacity:0;font-weight:400}
h2:hover .anchor,h3:hover .anchor,h4:hover .anchor,.anchor:focus{opacity:1}
@media (max-width:760px){.anchor{display:none}}
article p,article li{overflow-wrap:break-word}
article ul,article ol{padding-left:1.4em}
article li{margin:4px 0}
article blockquote{margin:16px 0;padding:2px 16px;border-left:3px solid var(--line-strong);color:var(--muted)}
article hr{border:0;border-top:1px solid var(--line);margin:40px 0}
.table-wrap{margin:20px 0 24px;overflow-x:auto;border:1px solid var(--line);border-radius:10px;background:var(--surface)}
@media (min-width:1100px){.table-wrap{width:max-content;max-width:min(980px,calc(100vw - 360px));min-width:100%}}
.table-wrap:focus{outline:2px solid var(--accent);outline-offset:2px}
table{border-collapse:collapse;font-size:14.5px;line-height:1.5;width:auto;min-width:100%}
th,td{padding:9px 12px;text-align:left;vertical-align:top;border-bottom:1px solid var(--line)}
th{color:var(--strong);font-weight:600;background:var(--row);white-space:nowrap}
td{min-width:9ch}
td:first-child{min-width:14ch}
tbody tr:last-child td{border-bottom:0}
tbody tr:nth-child(even) td{background:var(--row)}
.list{max-width:72ch;padding:28px 0 0}
.list .lede{color:var(--muted);margin:0 0 32px}
.card{display:block;border:1px solid var(--line);border-radius:14px;background:var(--surface);padding:22px 22px 18px;margin:0 0 18px;color:inherit;text-decoration:none}
.card:hover{border-color:var(--line-strong)}
.card .kicker{font-size:12.5px;color:var(--muted);letter-spacing:.04em;text-transform:uppercase;margin:0 0 6px}
.card h2{font-size:21px;line-height:1.3;color:var(--strong);margin:0 0 10px}
.card p{margin:0 0 10px;font-size:15.5px}
.card .meta{font-size:13px;color:var(--muted);margin:0}
.card .sections{font-size:13.5px;color:var(--muted);margin:8px 0 10px;padding:0;list-style:none;columns:2;column-gap:24px}
@media (max-width:560px){.card .sections{columns:1}}
`;

const tocScript = `
(function(){
  var links=[].slice.call(document.querySelectorAll('.toc a'));
  if(!links.length||!('IntersectionObserver' in window))return;
  var map={};links.forEach(function(a){map[a.getAttribute('href').slice(1)]=a});
  var heads=links.map(function(a){return document.getElementById(a.getAttribute('href').slice(1))}).filter(Boolean);
  var current=null;
  function set(id){if(current)current.classList.remove('on');current=map[id];if(current){current.classList.add('on');var t=current.closest('.toc');var r=current.getBoundingClientRect(),tr=t.getBoundingClientRect();if(r.top<tr.top||r.bottom>tr.bottom)current.scrollIntoView({block:'nearest'});}}
  function pick(){var y=window.innerHeight*0.25,best=heads[0];for(var i=0;i<heads.length;i++){if(heads[i].getBoundingClientRect().top<=y)best=heads[i];else break;}if(best&&(!current||map[best.id]!==current))set(best.id);}
  var raf=0;window.addEventListener('scroll',function(){if(!raf)raf=requestAnimationFrame(function(){raf=0;pick();});},{passive:true});pick();
})();`;

const figureScript = (key) => `
(function(){
  var f=document.getElementById('fig');
  var mq=window.matchMedia('(prefers-color-scheme: light)');
  function src(){return 'figures/${key}/'+(mq.matches?'index-light.html':'index.html');}
  function fit(){try{var d=f.contentDocument;if(!d||!d.documentElement)return;var h=Math.max(d.documentElement.scrollHeight,d.body?d.body.scrollHeight:0);if(h>0)f.style.height=h+'px';}catch(e){}}
  f.addEventListener('load',function(){fit();try{var d=f.contentDocument;if('ResizeObserver' in window){new ResizeObserver(fit).observe(d.body||d.documentElement);}}catch(e){}});
  window.addEventListener('resize',fit);
  f.src=src();
  var on=function(){f.src=src();};
  if(mq.addEventListener)mq.addEventListener('change',on);else if(mq.addListener)mq.addListener(on);
})();`;

function page({ title, body, current, extraHead = "" }) {
  const nav = [`<a href="index.html"${current === "index" ? ' class="here"' : ""}>Pricing directions</a>`]
    .concat(proposals.map((p) => `<a href="${p.key}.html"${current === p.key ? ' class="here"' : ""}>${p.n}. ${esc(shortTitle(p))}</a>`))
    .join("");
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>${esc(title)}</title>
<style>${css}</style>
${extraHead}
</head>
<body>
<nav class="top" aria-label="Pages">${nav}</nav>
${body}
</body>
</html>
`;
}

const short = { "price-level": "Price level", "third-party-pays": "Someone else pays", "pause-idle": "Resting idle machines" };
const shortTitle = (p) => short[p.key];

export function build() {
  mkdirSync(dist, { recursive: true });
  const cards = [];
  for (const p of proposals) {
    const fig = join(here, p.key, "build.mjs");
    if (existsSync(fig)) {
      for (const theme of [[], ["--light"]]) execFileSync(process.execPath, [fig, ...theme], { stdio: "ignore" });
    }
    const md = readFileSync(p.src, "utf8");
    const r = render(md);
    const plainTitle = unesc(strip(r.title)).replace(/\s*\(proposal, [^)]*\)\s*$/, "");
    const hasFig = existsSync(join(here, p.key, "index.html"));
    if (hasFig) {
      mkdirSync(join(dist, "figures", p.key), { recursive: true });
      for (const f of ["index.html", "index-light.html"]) {
        const from = join(here, p.key, f);
        if (existsSync(from)) copyFileSync(from, join(dist, "figures", p.key, f));
      }
    }
    const tocHtml = r.toc
      .filter((t) => t.depth === 2 || t.depth === 3)
      .map((t) => `<li class="d${t.depth}"><a href="#${t.id}">${strip(t.html)}</a></li>`)
      .join("\n");
    const body = `<div class="layout">
<aside class="toc" aria-label="Contents"><h2>Contents</h2><ol>${tocHtml}</ol></aside>
<main>
<header class="doc"><p class="kicker">Direction ${p.n} &middot; proposal, 2026-10-11</p><h1>${esc(plainTitle)}</h1></header>
${hasFig ? `<figure class="figure"><iframe id="fig" title="Figure: ${esc(plainTitle)}" loading="eager"></iframe></figure>
<script>${figureScript(p.key)}</script>` : ""}
<article>
${r.html}
</article>
</main>
</div>
<script>${tocScript}</script>`;
    writeFileSync(join(dist, `${p.key}.html`), page({ title: shortTitle(p), body, current: p.key }));
    const sections = r.toc.filter((t) => t.depth === 2).map((t) => `<li>${strip(t.html)}</li>`).join("");
    cards.push(`<a class="card" href="${p.key}.html">
<p class="kicker">Direction ${p.n}</p>
<h2>${esc(plainTitle)}</h2>
<p>${r.lead}</p>
<ul class="sections">${sections}</ul>
<p class="meta">${r.words.toLocaleString("en-US")} words, about ${Math.round(r.words / 230)} min</p>
</a>`);
  }
  const index = `<div class="layout" style="grid-template-columns:minmax(0,1fr)">
<main class="list">
<header class="doc" style="padding-top:0"><p class="kicker">repose &middot; 2026-10-11</p><h1>Pricing directions</h1></header>
<p class="lede">Three proposals, each with its figure. All are analysis only and uncommitted.</p>
${cards.join("\n")}
</main>
</div>`;
  writeFileSync(join(dist, "index.html"), page({ title: "Pricing directions", body: index, current: "index" }));
  return proposals.map((p) => `${p.key}.html`).concat("index.html");
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const pages = build();
  console.log(`built ${pages.length} pages into ${dist}`);
}
