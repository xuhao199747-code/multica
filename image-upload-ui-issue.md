## Goal

Make chat image attachment interaction match the expected Codex-style experience. This is not image generation and not merely a generic file picker.

## Scope

- Keep the existing file attachment capability.
- Let a user select or paste multiple image files in one action.
- Render pending image attachments as visible thumbnails in the chat composer before sending.
- Arrange thumbnails compactly in a horizontal, wrapping strip that works on narrow widths.
- Provide a clear, accessible remove control on every pending image so one image can be removed without clearing the others.
- Preserve the chosen image order when the message is sent.
- Keep non-image attachments usable and visually distinct from image thumbnails.
- Support retry/error state without silently dropping pending attachments.

## Acceptance Criteria

- Selecting three images shows three independent thumbnails in the composer before submit.
- Pasting or dropping multiple images produces the same pending attachment UI.
- Clicking the remove control on the second thumbnail removes only that image.
- Sending a message includes all remaining images in their original order.
- Existing single-file and non-image attachment flows continue to work.
- Relevant component tests cover multi-image selection, individual removal, and send payload ordering.

## Out of Scope

- AI image generation.
- Changing storage backends or attachment authorization rules unless required to fix a discovered defect.
