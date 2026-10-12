# Explainers

Figures for proposals in `docs/proposals/`, and a site to read the two
together. Each folder holds one figure's `build.mjs`, named after its
proposal. `kit/` is the anatomy skill's isometric kit (MIT, `kit/LICENSE`).

```
node build.mjs              # builds every figure, renders dist/
node serve.mjs --watch      # serves dist on 0.0.0.0:4321, rebuilds on change
```

`build.mjs` reads Markdown with `apps/web`'s `marked`, so run `pnpm install`
first. Generated pages and `dist/` are not committed.
