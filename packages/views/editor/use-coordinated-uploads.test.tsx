// @vitest-environment jsdom
import { useEffect, useLayoutEffect, useMemo, useRef, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { DraftUpload } from "@multica/core/drafts";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";
import type { Attachment } from "@multica/core/types";
import enCommon from "../locales/en/common.json";
import enEditor from "../locales/en/editor.json";
import { markPastedTextFile, PASTED_TEXT_FILENAME } from "./extensions/file-upload";
import type { ContentEditorRef } from "./content-editor";
import type { UploadGate } from "./use-upload-gate";
import {
  useCoordinatedUploads,
  __liveEditorRegistryKeysForTest,
  type UploadDraftBinding,
} from "./use-coordinated-uploads";

// The coordinator (MUL-5181 L2) is what actually performs the request; tests
// drive an upload's outcome by controlling THAT promise.
const mockApiUploadFile = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({ api: { uploadFile: mockApiUploadFile } }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const TEST_RESOURCES = { en: { common: enCommon, editor: enEditor } };

const inertGate: UploadGate = {
  uploading: false,
  onUploadingChange: () => {},
  isBlocked: () => false,
};

function makeBinding(key: string): UploadDraftBinding {
  return {
    registryKey: key,
    getUploads: () => [],
    addUpload: () => {},
    settleUpload: () => {},
    failUpload: () => {},
    removeUpload: () => {},
    getBody: () => "",
    appendToBody: () => {},
  };
}

function HookHost({ registryKey }: { registryKey: string }) {
  const editorRef = useRef<ContentEditorRef | null>(null);
  const binding = useMemo(() => makeBinding(registryKey), [registryKey]);
  useCoordinatedUploads(binding, [], {}, inertGate, editorRef);
  return null;
}

/**
 * Captures the registry from a PARENT layout effect: layout effects run
 * child-first within a commit, so at capture time the hook's registration for
 * this commit must already be visible. A passive registration would not be —
 * it flushes a task later, which is exactly the settle window where a
 * write-back for the old key can insert into an editor already holding the
 * new draft's document (chat's pinned-switch adopts in layout).
 */
function CaptureAfterCommit({
  registryKey,
  capture,
  children,
}: {
  registryKey: string;
  capture: (keys: string[]) => void;
  children: ReactNode;
}) {
  useLayoutEffect(() => {
    capture(__liveEditorRegistryKeysForTest());
  }, [registryKey, capture]);
  return <>{children}</>;
}

function Probe({ registryKey, capture }: { registryKey: string; capture: (keys: string[]) => void }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <CaptureAfterCommit registryKey={registryKey} capture={capture}>
        <HookHost registryKey={registryKey} />
      </CaptureAfterCommit>
    </I18nProvider>
  );
}

describe("useCoordinatedUploads live-editor registry timing", () => {
  it("registers within the commit (layout), so a key switch is visible before any task runs", () => {
    const captures: string[][] = [];
    const capture = (keys: string[]) => captures.push(keys);

    const view = render(<Probe registryKey="probe:a" capture={capture} />);
    expect(captures.at(-1)).toContain("probe:a");

    view.rerender(<Probe registryKey="probe:b" capture={capture} />);

    // At the parent's layout effect of the SWITCH commit, the registry must
    // already point at the new key and no longer at the old one. A passive
    // registration fails both assertions here.
    expect(captures.at(-1)).toContain("probe:b");
    expect(captures.at(-1)).not.toContain("probe:a");

    view.unmount();
    expect(__liveEditorRegistryKeysForTest()).not.toContain("probe:b");
  });
});

/**
 * A store-backed binding, in memory. Real enough for the write-back paths,
 * which only ever go through these accessors — and, like the real ones, still
 * callable after the component that started the upload is gone.
 */
function makeRecordingBinding(key: string) {
  const state = { uploads: [] as DraftUpload[], body: "" };
  const binding: UploadDraftBinding = {
    registryKey: key,
    getUploads: () => state.uploads,
    addUpload: (upload) => {
      state.uploads = [...state.uploads, upload];
    },
    settleUpload: () => {},
    failUpload: () => {},
    removeUpload: (clientUploadId) => {
      state.uploads = state.uploads.filter((u) => u.clientUploadId !== clientUploadId);
    },
    getBody: () => state.body,
    appendToBody: (markdown, options) => {
      const joiner = options?.cohesive ? "\n" : "\n\n";
      state.body = state.body ? `${state.body.replace(/\s+$/, "")}${joiner}${markdown}` : markdown;
    },
  };
  return { binding, state };
}

/** Drive one real upload to success and report what write-back landed. */
async function uploadOne(
  upload: (file: File) => Promise<UploadResult | null>,
  file: File,
  attachment: Attachment,
): Promise<void> {
  mockApiUploadFile.mockImplementationOnce(() => Promise.resolve(attachment));
  await act(async () => {
    await upload(file);
  });
}

function UploadHost({
  binding,
  expose,
}: {
  binding: UploadDraftBinding;
  expose: (upload: (file: File) => Promise<UploadResult | null>) => void;
}) {
  const editorRef = useRef<ContentEditorRef | null>(null);
  const { handleUpload } = useCoordinatedUploads(binding, [], {}, inertGate, editorRef);
  useEffect(() => {
    expose(handleUpload);
  }, [handleUpload, expose]);
  return null;
}

/**
 * Paste-as-file recovery lives here, not in a composer test, because the hook
 * is the single responder for it: the upload outlives its mount, so no editor
 * can own the failure path. Only chat opts into paste-as-file today
 * (`pasteAsFileThreshold`), but the recovery contract is the hook's regardless
 * of which surface turns the feature on.
 */
describe("useCoordinatedUploads paste-as-file recovery", () => {
  it("puts a failed paste's source text back into the persisted body after the mount dies", async () => {
    const pastedText = "a long pasted wall of text that became an attachment";
    const { binding, state } = makeRecordingBinding("probe:paste");
    let upload!: (file: File) => Promise<UploadResult | null>;

    let rejectUpload!: (err: Error) => void;
    mockApiUploadFile.mockImplementationOnce(
      () =>
        new Promise<Attachment>((_resolve, reject) => {
          rejectUpload = reject;
        }),
    );

    const view = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <UploadHost
          binding={binding}
          expose={(fn) => {
            upload = fn;
          }}
        />
      </I18nProvider>,
    );

    await act(async () => {
      void upload(
        markPastedTextFile(
          new File([pastedText], PASTED_TEXT_FILENAME, { type: "text/plain" }),
          pastedText,
        ),
      );
    });
    expect(state.uploads).toHaveLength(1);

    view.unmount();
    await act(async () => {
      rejectUpload(new Error("upload failed"));
      await Promise.resolve();
    });

    // A dropped file survives a failed upload on disk; this text does not exist
    // anywhere else — it was never written into the document — so the draft
    // must get it back rather than keep a chip standing in for content the
    // user can never recover.
    expect(state.body).toContain(pastedText);
    expect(state.uploads).toHaveLength(0);
  });
});

describe("useCoordinatedUploads consecutive-image write-back (DAWE-2)", () => {
  const imageFile = (name: string) => new File(["x"], name, { type: "image/png" });
  const imageAttachment = (id: string, name: string): Attachment =>
    ({
      id,
      filename: name,
      content_type: "image/png",
      size: 1,
      url: `/api/attachments/${id}/download`,
      created_at: new Date().toISOString(),
    }) as unknown as Attachment;

  it("joins consecutive finished images with a single newline so they form one image run", async () => {
    const { binding, state } = makeRecordingBinding("probe:images");
    let upload!: (file: File) => Promise<UploadResult | null>;
    let rejectUpload!: (err: Error) => void;

    // The write-back path only runs for a mount that is GONE by settle time
    // (a live mount does the inline editor swap instead), so start the
    // upload, unmount, then resolve — same shape as the paste test above.
    mockApiUploadFile.mockImplementationOnce(
      () =>
        new Promise<Attachment>((_resolve, reject) => {
          rejectUpload = reject;
        }),
    );
    const view = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <UploadHost
          binding={binding}
          expose={(fn) => {
            upload = fn;
          }}
        />
      </I18nProvider>,
    );

    await act(async () => {
      void upload(imageFile("a.png"));
    });
    view.unmount();
    await act(async () => {
      rejectUpload(new Error("stop"));
      await Promise.resolve();
    });
    // Placeholder is gone; the next uploads append onto the persisted body.
    expect(state.uploads).toHaveLength(0);
    await uploadOne(upload, imageFile("a.png"), imageAttachment("att-a", "a.png"));
    await uploadOne(upload, imageFile("b.png"), imageAttachment("att-b", "b.png"));
    await uploadOne(upload, imageFile("c.png"), imageAttachment("att-c", "c.png"));

    // No live editor is registered for the key, so every settle lands via
    // appendToBody. Consecutive images must share ONE markdown block — a
    // blank line between them would split the run and the readonly
    // image-grid transform would render them stacked instead of grouped.
    expect(state.body).toBe(
      "![a.png](/api/attachments/att-a/download)\n" +
        "![b.png](/api/attachments/att-b/download)\n" +
        "![c.png](/api/attachments/att-c/download)",
    );

    view.unmount();
  });

  it("starts a new block when prose precedes the image run", async () => {
    const { binding, state } = makeRecordingBinding("probe:images-prose");
    state.body = "see these";
    let upload!: (file: File) => Promise<UploadResult | null>;
    let rejectUpload!: (err: Error) => void;

    mockApiUploadFile.mockImplementationOnce(
      () =>
        new Promise<Attachment>((_resolve, reject) => {
          rejectUpload = reject;
        }),
    );
    const view = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <UploadHost
          binding={binding}
          expose={(fn) => {
            upload = fn;
          }}
        />
      </I18nProvider>,
    );

    await act(async () => {
      void upload(imageFile("warm.png"));
    });
    view.unmount();
    await act(async () => {
      rejectUpload(new Error("stop"));
      await Promise.resolve();
    });
    expect(state.uploads).toHaveLength(0);
    await uploadOne(upload, imageFile("a.png"), imageAttachment("att-a", "a.png"));
    await uploadOne(upload, imageFile("b.png"), imageAttachment("att-b", "b.png"));

    // First image after prose opens a new paragraph; the second still joins.
    expect(state.body).toBe(
      "see these\n\n![a.png](/api/attachments/att-a/download)\n![b.png](/api/attachments/att-b/download)",
    );

    view.unmount();
  });
});
