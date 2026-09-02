import { Link, createFileRoute } from "@tanstack/react-router";

import { AppService } from "../../bindings/github.com/otis-http/otis/internal/services";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export const Route = createFileRoute("/about")({
  loader: async () => {
    const version = await AppService.Version();
    return { version };
  },
  component: AboutPage,
});

function AboutPage() {
  const { version } = Route.useLoaderData();

  return (
    <main className="flex min-h-screen items-center justify-center p-8">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>About Otis</CardTitle>
          <CardDescription>File-based HTTP client.</CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            Version <span className="font-mono text-foreground">{version}</span>
          </p>
        </CardContent>
        <CardFooter>
          <Button asChild variant="outline">
            <Link to="/">Home</Link>
          </Button>
        </CardFooter>
      </Card>
    </main>
  );
}
