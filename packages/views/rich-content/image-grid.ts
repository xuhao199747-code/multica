/**
 * Rehype plugin — group consecutive images in a readonly body into one
 * <div data-type="imageGrid"> container (DAWE-2).
 *
 * Markdown gives every image its own block: `![](...)` lines that follow each
 * other land in one <p> separated by <br> (remarkBreaks), and blank-line-
 * separated images land in sibling <p>s. Either way each image used to render
 * as its own full-width row, so a message of screenshots read as an endless
 * vertical tape. This plugin rewrites the hast AFTER rehype-sanitize (the
 * injected container is not part of the sanitize allow-list) so a run of
 * adjacent image blocks collapses into a single container the div renderer
 * lays out as a wrapping flex row — the Codex / ChatGPT message layout.
 *
 * Grouping only changes the DOM: the image order inside the tree is
 * preserved, and the lightbox sequence is built from the message data by
 * collectAttachmentSequence, not from the DOM — so preview paging order is
 * unaffected (the constraint noted on orderStandaloneAttachments).
 *
 * A child counts as an image unit when it is:
 *   - an <img> element, or
 *   - a <p> whose children are only whitespace, <br> and <img> with at least
 *     one image — i.e. a paragraph that exists only to hold images.
 * Whitespace text between units is absorbed into the group. Anything else —
 * prose, other elements, a paragraph mixing text and images — breaks the run
 * and is left untouched.
 *
 * Pure hast manipulation — no DOM, no React. Runs after sanitize on every
 * render, including while a message is streaming; a run simply grows or
 * shrinks as new image lines arrive.
 */

import type { Element, ElementContent, Root } from "hast";

export const IMAGE_GRID_DATA_TYPE = "imageGrid";

function isWhitespaceText(node: ElementContent): boolean {
  return node.type === "text" && /^\s*$/.test(node.value);
}

function isBreakElement(node: ElementContent): boolean {
  return node.type === "element" && node.tagName === "br";
}

/**
 * The images a child contributes to a group, or null when the child is not
 * an image unit. A bare <img> contributes itself; an image-only <p>
 * contributes each of its images in order.
 */
function imageUnitImages(node: ElementContent): Element[] | null {
  if (node.type === "element" && node.tagName === "img") return [node];
  if (node.type !== "element" || node.tagName !== "p") return null;
  const images: Element[] = [];
  for (const child of node.children) {
    if (isWhitespaceText(child) || isBreakElement(child)) continue;
    if (child.type === "element" && child.tagName === "img") {
      images.push(child);
      continue;
    }
    // Text, an <a>-wrapped image, or any other content — the paragraph is
    // not an image-only block, so it never joins a group.
    return null;
  }
  return images.length > 0 ? images : null;
}

function isImageGridContainer(node: ElementContent): boolean {
  return (
    node.type === "element" &&
    node.tagName === "div" &&
    node.properties?.dataType === IMAGE_GRID_DATA_TYPE
  );
}

/**
 * Collapse every maximal run of adjacent image units in `children` into one
 * container div. Runs of a single image are still wrapped, so all readonly
 * inline images share one DOM shape.
 */
function groupImageRuns(children: ElementContent[]): void {
  const runs: Array<{ start: number; end: number; images: Element[] }> = [];
  let i = 0;
  while (i < children.length) {
    const first = imageUnitImages(children[i]!);
    if (!first) {
      i += 1;
      continue;
    }
    let end = i + 1;
    const images = [...first];
    while (end < children.length) {
      if (isWhitespaceText(children[end]!)) {
        end += 1;
        continue;
      }
      const next = imageUnitImages(children[end]!);
      if (!next) break;
      images.push(...next);
      end += 1;
    }
    runs.push({ start: i, end, images });
    i = end;
  }
  for (const run of runs.reverse()) {
    const grid: Element = {
      type: "element",
      tagName: "div",
      properties: { dataType: IMAGE_GRID_DATA_TYPE },
      children: run.images,
    };
    children.splice(run.start, run.end - run.start, grid);
  }
}

/**
 * The transform behind the rehype plugin, exported for direct unit tests:
 * hand-built hast fixtures in, grouped tree out.
 */
export function groupImagesInHast(tree: Root): void {
  const walk = (node: Root | Element): void => {
    groupImageRuns(node.children as ElementContent[]);
    for (const child of node.children) {
      // A container's children were already grouped — descending again would
      // re-wrap them forever.
      if (child.type !== "element" || isImageGridContainer(child)) continue;
      walk(child);
    }
  };
  walk(tree);
}

/** Rehype plugin factory — add AFTER rehypeSanitize in the plugin chain. */
export default function rehypeImageGrid() {
  return (tree: Root) => groupImagesInHast(tree);
}
