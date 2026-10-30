// @vitest-environment node

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { describe, it } from "node:test";
import { fileURLToPath } from "node:url";

const baseStyles = readFileSync(
  fileURLToPath(new URL("./base.css", import.meta.url)),
  "utf8",
);

describe("base styles", () => {
  it("paints the root canvas with the active theme background", () => {
    assert.match(baseStyles, /html\s*\{\s*@apply\s+bg-background\s+font-sans;/);
  });
});
