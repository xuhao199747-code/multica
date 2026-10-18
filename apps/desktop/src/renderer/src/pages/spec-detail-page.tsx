import { useParams } from "react-router-dom";
import { SpecDetail } from "@multica/views/specs";

export function SpecDetailPage() {
  const { id } = useParams<{ id: string }>();
  if (!id) return null;
  return <SpecDetail id={id} />;
}
