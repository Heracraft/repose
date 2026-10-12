// From the anatomy skill's scripts/inline-kit.mjs (MIT, see LICENSE): the
// kit as source to inline into a standalone page.
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const KIT = dirname(fileURLToPath(import.meta.url));

export function kitScript() {
  return readFileSync(join(KIT, "iso-kit.mjs"), "utf8").replace(/^export /gm, "");
}
