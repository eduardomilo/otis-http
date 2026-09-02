import { createFileRoute } from "@tanstack/react-router";

import { EnvironmentEditor } from "@/components/environment/environment-editor";

export const Route = createFileRoute("/env/$name")({
  component: EnvironmentView,
});

/** The environment editor (screen 1c). $name is the environment's name. */
function EnvironmentView() {
  const { name } = Route.useParams();
  return <EnvironmentEditor name={name} />;
}
