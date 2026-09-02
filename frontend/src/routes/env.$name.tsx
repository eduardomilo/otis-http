import { createFileRoute } from "@tanstack/react-router";

import { Placeholder } from "@/components/shell/placeholder";

export const Route = createFileRoute("/env/$name")({
  component: EnvironmentView,
});

/** The environment editor (screen 1c). $name is the environment's name. */
function EnvironmentView() {
  const { name } = Route.useParams();
  return <Placeholder kind="Environment" path={`env/${name}.json`} phase="Phase C" />;
}
