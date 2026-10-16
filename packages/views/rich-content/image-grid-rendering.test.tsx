/**
 * Consecutive-image grid (DAWE-2) — the readonly renderer groups adjacent
 * message images into one wrapping thumbnail grid instead of stacking
 * full-width rows.
 *
 * The Attachment dispatcher is mocked to record what each inline image
 * received: the contract under test is RichContent's own — the rehype plugin
 * grouping (unit-tested in image-grid.test.ts), the imageGrid div branch,
 * the thumbnail layout, and the original upload order. The real Attachment
 * classes are pinned by the AttachmentList suite in comment-card.test.tsx.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { collectAttachmentSequence } from "@multica/core/attachments/image-sequence";

const imageRenders: Array<{ url: string; layout?: string }> = [];

vi.mock("../issues/hooks", () => ({
  useResolveIssueIdentifier: () => null,
}));

vi.mock("../i18n", async () => {
  const editor = (await import("../locales/en/editor.json")).default;
  return {
    useT: () => ({
      t: (select: (bundle: typeof editor) => string) => select(editor),
    }),
    useTimeAgo: () => "just now",
  };
});

vi.mock("@multica/core/api", () => ({
  api: { getAttachmentTextContent: vi.fn() },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
  }),
  useWorkspaceSlug: () => "acme",
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: vi.fn(), openInNewTab: vi.fn() }),
  useOptionalNavigation: () => ({ push: vi.fn(), openInNewTab: vi.fn() }),
  resolveClickIntent: () => "push",
  useAppOrigin: () => null,
  AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));

vi.mock("../issues/components/issue-mention-card", () => ({
  IssueMentionCard: ({ issueId }: { issueId: string }) => <span>{issueId}</span>,
}));

vi.mock("../projects/components/project-chip", () => ({
  ProjectChip: ({ projectId }: { projectId: string }) => <span>{projectId}</span>,
}));

vi.mock("../editor/link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));

vi.mock("../editor/attachment", () => ({
  Attachment: ({
    attachment,
    layout,
  }: {
    attachment: { url: string; filename: string };
    layout?: string;
  }) => {
    imageRenders.push({ url: attachment.url, layout });
    return (
      <img
        data-testid="inline-image"
        data-layout={layout}
        src={attachment.url}
        alt={attachment.filename}
      />
    );
  },
}));

import { RichContent } from "./rich-content";

const URL_1 = "https://cdn.test/1.png";
const URL_2 = "https://cdn.test/2.png";
const URL_3 = "https://cdn.test/3.png";

beforeEach(() => {
  imageRenders.length = 0;
});

describe("RichContent image grid (DAWE-2)", () => {
  it("groups images on consecutive lines (one paragraph, <br> between) into a single grid", () => {
    const { container } = render(
      <RichContent content={`![one](${URL_1})\n![two](${URL_2})`} />,
    );
    const grids = container.querySelectorAll(".image-grid");
    expect(grids).toHaveLength(1);
    const images = [...grids[0]!.querySelectorAll("img")].map((el) =>
      el.getAttribute("src"),
    );
    expect(images).toEqual([URL_1, URL_2]);
  });

  it("groups blank-line-separated image paragraphs into a single grid", () => {
    const { container } = render(
      <RichContent content={`![one](${URL_1})\n\n![two](${URL_2})\n\n![three](${URL_3})`} />,
    );
    const grids = container.querySelectorAll(".image-grid");
    expect(grids).toHaveLength(1);
    const images = [...grids[0]!.querySelectorAll("img")].map((el) =>
      el.getAttribute("src"),
    );
    expect(images).toEqual([URL_1, URL_2, URL_3]);
  });

  it("renders every inline image as a fixed-height thumbnail, not the full-width block layout", () => {
    render(<RichContent content={`![one](${URL_1})\n\n![two](${URL_2})`} />);
    expect(imageRenders.map((r) => r.layout)).toEqual(["thumb", "thumb"]);
  });

  it("breaks the grid at a prose paragraph between images", () => {
    const { container } = render(
      <RichContent
        content={`![one](${URL_1})\n\nnotes in between\n\n![two](${URL_2})`}
      />,
    );
    const grids = container.querySelectorAll(".image-grid");
    expect(grids).toHaveLength(2);
    expect(grids[0]!.querySelector("img")?.getAttribute("src")).toBe(URL_1);
    expect(grids[1]!.querySelector("img")?.getAttribute("src")).toBe(URL_2);
    expect(container.textContent).toContain("notes in between");
  });

  it("wraps a lone image after prose in its own grid, keeping the prose outside", () => {
    const { container } = render(
      <RichContent content={`look at this:\n\n![one](${URL_1})`} />,
    );
    const grids = container.querySelectorAll(".image-grid");
    expect(grids).toHaveLength(1);
    expect(grids[0]!.querySelectorAll("img")).toHaveLength(1);
    // The prose paragraph stays a sibling of the grid, not inside it.
    const prose = [...container.querySelectorAll("p")].find((el) =>
      el.textContent?.includes("look at this:"),
    );
    expect(prose).toBeTruthy();
    expect(prose?.closest(".image-grid")).toBeNull();
    expect(imageRenders).toEqual([{ url: URL_1, layout: "thumb" }]);
  });

  it("keeps the DOM image order aligned with the lightbox sequence order", () => {
    const content = `intro text\n\n![one](${URL_1})\n\n![two](${URL_2})\n\n![three](${URL_3})`;
    const { container } = render(<RichContent content={content} />);
    const domOrder = [...container.querySelectorAll(".image-grid img")].map((el) =>
      el.getAttribute("src"),
    );
    const sequence = collectAttachmentSequence([{ content }], () => true);
    expect(sequence.map((item) => item.url)).toEqual(domOrder);
  });
});
