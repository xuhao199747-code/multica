"use client";
import { use } from "react";
import { SpecDetail } from "@multica/views/specs";

export default function SpecDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  return <SpecDetail id={id} />;
}
