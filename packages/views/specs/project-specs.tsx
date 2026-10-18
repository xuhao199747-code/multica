"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { specListOptions, useCreateSpec } from "@multica/core/specs";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink, useNavigation } from "../navigation";
import { useT } from "../i18n";

export function ProjectSpecs({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { data, isLoading, isError } = useQuery(specListOptions(wsId, projectId));
  const create = useCreateSpec();
  const [creating, setCreating] = useState(false);
  const [title, setTitle] = useState("");
  return <section className="space-y-2">
    <div className="flex items-center justify-between"><h2 className="text-caption font-medium">{t($ => $.specs.title)}</h2><Button size="xs" variant="ghost" onClick={() => setCreating(!creating)}>{t($ => $.specs.new)}</Button></div>
    {creating && <form className="space-y-2" onSubmit={(event) => { event.preventDefault(); void create.mutateAsync({ title: title.trim(), project_id: projectId }).then((spec) => navigation.push(paths.specDetail(spec.id))).catch(() => {}); }}>
      <label className="block text-caption">{t($ => $.specs.title_label)}<input className="mt-1 w-full rounded border bg-background px-2 py-1" value={title} onChange={event => setTitle(event.target.value)} /></label>
      <Button type="submit" size="xs" disabled={!title.trim() || create.isPending}>{t($ => $.specs.create)}</Button>
      {create.isError && <p role="alert">{t($ => $.specs.create_error)}</p>}
    </form>}
    {isError && <p role="alert">{t($ => $.specs.load_error)}</p>}
    {!isLoading && !isError && !data?.specs.length && <p className="text-caption text-muted-foreground">{t($ => $.specs.empty)}</p>}
    {data?.specs.map(spec => <AppLink key={spec.id} href={paths.specDetail(spec.id)} className="block rounded px-2 py-1 text-caption hover:bg-accent"><span className="text-muted-foreground">{spec.identifier}</span>{" "}{spec.title}<span className="block text-muted-foreground">{spec.status === "draft" ? t($ => $.specs.status_draft) : spec.status === "in_review" ? t($ => $.specs.status_in_review) : spec.status === "approved" ? t($ => $.specs.status_approved) : spec.status === "deprecated" ? t($ => $.specs.status_deprecated) : spec.status} · {spec.current_version_number ? t($ => $.specs.version_number, { number: spec.current_version_number }) : t($ => $.specs.no_versions)} · <time dateTime={spec.updated_at}>{new Date(spec.updated_at).toLocaleDateString()}</time></span></AppLink>)}
  </section>;
}
