import React from "react";
import ReactDOM from "react-dom/client";
import { RouterProvider, createRouter } from "@tanstack/react-router";
import { createHashHistory } from "@tanstack/history";
import { Events } from "@wailsio/runtime";

import { routeTree } from "./routeTree.gen";
import "./index.css";

// Register the Go -> frontend event listener at module scope, before React
// renders. The Wails runtime signals "ready" to Go when it is imported above,
// and Go answers with app:ready; registering here guarantees the listener
// exists before that answer can arrive.
Events.On("app:ready", (event) => {
  console.log("[otis] app:ready", { version: event.data });
});

// Hash history is REQUIRED. The packaged app is served from a custom scheme
// (wails://) where browser history routes 404 on reload / deep link.
const router = createRouter({
  routeTree,
  history: createHashHistory(),
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <RouterProvider router={router} />
  </React.StrictMode>,
);
