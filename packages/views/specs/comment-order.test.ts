// @vitest-environment node
import { describe, expect, it } from "vitest";
import { orderSpecComments } from "./comment-order";

describe("spec comment threads", () => {
  it("keeps replies with their parent, even when another thread was created between them", () => {
    const comments = [
      { id: "a", parent_id: null }, { id: "b", parent_id: null },
      { id: "a1", parent_id: "a" }, { id: "a2", parent_id: "a1" },
      { id: "missing", parent_id: "gone" },
    ];
    expect(orderSpecComments(comments).map(({ comment, depth }) => [comment.id, depth])).toEqual([
      ["a", 0], ["a1", 1], ["a2", 2], ["b", 0], ["missing", 0],
    ]);
  });
});
