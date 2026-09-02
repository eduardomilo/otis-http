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

export const Route = createFileRoute("/")({
  // Calling a Go binding from a loader proves bindings work outside of
  // event handlers, which the real app relies on for route data.
  loader: async () => {
    const pong = await AppService.Ping("hello from loader");
    return { pong };
  },
  component: IndexPage,
});

function IndexPage() {
  const { pong } = Route.useLoaderData();

  return (
    <main className="flex min-h-screen items-center justify-center p-8">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Otis</CardTitle>
          <CardDescription>Plumbing check: Go binding called from a route loader.</CardDescription>
        </CardHeader>
        <CardContent>
          <p className="font-mono text-sm text-muted-foreground">{pong}</p>
        </CardContent>
        <CardFooter>
          <Button asChild>
            <Link to="/about">About</Link>
          </Button>
        </CardFooter>
      </Card>
    </main>
  );
}
