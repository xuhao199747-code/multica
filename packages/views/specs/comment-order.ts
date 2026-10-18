/** Group a chronological comment list by parent without dropping malformed or orphaned entries. */
export function orderSpecComments<T extends { id: string; parent_id: string | null }>(comments: T[]): { comment: T; depth: number }[] {
  const ids = new Set(comments.map(comment => comment.id));
  const children = new Map<string | null, T[]>();
  for (const comment of comments) {
    const parent = comment.parent_id && ids.has(comment.parent_id) ? comment.parent_id : null;
    const group = children.get(parent) ?? [];
    group.push(comment);
    children.set(parent, group);
  }
  const result: { comment: T; depth: number }[] = [];
  const visited = new Set<string>();
  const visit = (comment: T, depth: number) => {
    if (visited.has(comment.id)) return;
    visited.add(comment.id);
    result.push({ comment, depth });
    for (const reply of children.get(comment.id) ?? []) visit(reply, depth + 1);
  };
  for (const root of children.get(null) ?? []) visit(root, 0);
  for (const comment of comments) visit(comment, 0);
  return result;
}
