// @vitest-environment node
/**
 * Unit tests for the rehypeImageGrid hast transform (DAWE-2).
 *
 * The fixtures are hand-built hast trees shaped like what remarkBreaks +
 * rehypeRaw + rehypeSanitize produce for message markdown: images land in
 * <p>s, soft line breaks as <br>, blank-line-separated blocks as sibling <p>s.
 */
import { describe, expect, it } from "vitest";
import type { Element, ElementContent, Root, RootContent, Text } from "hast";
import { groupImagesInHast, IMAGE_GRID_DATA_TYPE } from "./image-grid";

function text(value: string): Text {
  return { type: "text", value };
}

function img(src: string): Element {
  return {
    type: "element",
    tagName: "img",
    properties: { src },
    children: [],
  };
}

function p(...children: ElementContent[]): Element {
  return { type: "element", tagName: "p", properties: {}, children };
}

function br(): Element {
  return { type: "element", tagName: "br", properties: {}, children: [] };
}

function root(...children: RootContent[]): Root {
  return { type: "root", children };
}

function gridImages(node: RootContent | undefined): string[] {
  if (node?.type !== "element") return [];
  return node.children
    .filter((child): child is Element => child.type === "element" && child.tagName === "img")
    .map((child) => String(child.properties?.src ?? ""));
}

describe("groupImagesInHast", () => {
  it("wraps a single image paragraph in one grid", () => {
    const tree = root(p(img("https://cdn.test/1.png")));
    groupImagesInHast(tree);
    expect(tree.children).toHaveLength(1);
    const grid = tree.children[0];
    expect(grid?.type === "element" && grid.properties?.dataType).toBe(IMAGE_GRID_DATA_TYPE);
    expect(gridImages(grid)).toEqual(["https://cdn.test/1.png"]);
  });

  it("groups images sharing one paragraph via <br> (remarkBreaks shape)", () => {
    const tree = root(p(img("https://cdn.test/1.png"), br(), img("https://cdn.test/2.png")));
    groupImagesInHast(tree);
    expect(tree.children).toHaveLength(1);
    expect(gridImages(tree.children[0])).toEqual([
      "https://cdn.test/1.png",
      "https://cdn.test/2.png",
    ]);
  });

  it("groups blank-line-separated image paragraphs into one grid", () => {
    const tree = root(
      p(img("https://cdn.test/1.png")),
      text("\n"),
      p(img("https://cdn.test/2.png")),
      p(img("https://cdn.test/3.png")),
    );
    groupImagesInHast(tree);
    expect(tree.children).toHaveLength(1);
    expect(gridImages(tree.children[0])).toEqual([
      "https://cdn.test/1.png",
      "https://cdn.test/2.png",
      "https://cdn.test/3.png",
    ]);
  });

  it("breaks the run at prose between images and keeps both grids", () => {
    const tree = root(
      p(img("https://cdn.test/1.png")),
      p(text("notes in between")),
      p(img("https://cdn.test/2.png")),
    );
    groupImagesInHast(tree);
    expect(tree.children).toHaveLength(3);
    expect(gridImages(tree.children[0])).toEqual(["https://cdn.test/1.png"]);
    expect(gridImages(tree.children[2])).toEqual(["https://cdn.test/2.png"]);
    const prose = tree.children[1];
    expect(prose?.type === "element" && prose.tagName).toBe("p");
  });

  it("leaves a paragraph mixing text and images untouched", () => {
    const tree = root(p(text("look:"), img("https://cdn.test/1.png")));
    groupImagesInHast(tree);
    expect(tree.children).toHaveLength(1);
    const paragraph = tree.children[0];
    expect(paragraph?.type === "element" && paragraph.tagName).toBe("p");
    expect(gridImages(paragraph)).toEqual([]);
  });

  it("groups inside nested containers such as blockquote without touching the wrapper", () => {
    const quote: Element = {
      type: "element",
      tagName: "blockquote",
      properties: {},
      children: [p(img("https://cdn.test/1.png")), p(img("https://cdn.test/2.png"))],
    };
    const tree = root(quote);
    groupImagesInHast(tree);
    expect(tree.children[0]).toBe(quote);
    expect(quote.children).toHaveLength(1);
    expect(gridImages(quote.children[0])).toEqual([
      "https://cdn.test/1.png",
      "https://cdn.test/2.png",
    ]);
  });

  it("does not group across list items", () => {
    const list: Element = {
      type: "element",
      tagName: "ul",
      properties: {},
      children: [
        { type: "element", tagName: "li", properties: {}, children: [p(img("https://cdn.test/1.png"))] },
        { type: "element", tagName: "li", properties: {}, children: [p(img("https://cdn.test/2.png"))] },
      ],
    };
    const tree = root(list);
    groupImagesInHast(tree);
    expect(tree.children[0]).toBe(list);
    expect(list.children).toHaveLength(2);
    const firstItem = list.children[0];
    // Each li keeps its own single-image grid; the run never crosses the li boundary.
    expect(firstItem?.type === "element" && firstItem.children).toHaveLength(1);
    expect(gridImages(list.children[0]?.type === "element" ? list.children[0].children[0] : undefined)).toEqual(["https://cdn.test/1.png"]);
    expect(gridImages(list.children[1]?.type === "element" ? list.children[1].children[0] : undefined)).toEqual(["https://cdn.test/2.png"]);
  });

  it("absorbs whitespace between units and keeps non-image content intact", () => {
    const tree = root(
      text("\n"),
      p(img("https://cdn.test/1.png")),
      text("\n\n"),
      p(img("https://cdn.test/2.png")),
      text("\n"),
      p(text("after")),
    );
    groupImagesInHast(tree);
    // Leading whitespace before the run stays; the whitespace around the
    // units is absorbed into the grid; the trailing prose paragraph is intact.
    expect(tree.children).toHaveLength(3);
    expect(tree.children[0]).toEqual(text("\n"));
    expect(gridImages(tree.children[1])).toEqual([
      "https://cdn.test/1.png",
      "https://cdn.test/2.png",
    ]);
    expect(tree.children[2]).toEqual(p(text("after")));
  });

  it("wraps a bare img child (raw HTML shape) and does not re-wrap its own grid", () => {
    const tree = root(img("https://cdn.test/1.png"), img("https://cdn.test/2.png"));
    groupImagesInHast(tree);
    expect(tree.children).toHaveLength(1);
    const grid = tree.children[0];
    expect(grid?.type === "element" && grid.tagName).toBe("div");
    expect(gridImages(grid)).toEqual(["https://cdn.test/1.png", "https://cdn.test/2.png"]);
    // Exactly one level of wrapping — the walk never descends into a grid.
    expect(grid?.type === "element" && grid.children.some((c) => c.type === "element" && c.tagName === "div")).toBe(false);
  });
});
