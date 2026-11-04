import * as React from "react";
import { RotateCw, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { IconTile } from "@/components/kind";

/**
 * ErrorBoundary keeps a rendering bug in one screen from leaving a blank page
 * with no way out — the durable state is all server-side, so a reload always
 * recovers.
 */
export class ErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  componentDidCatch(error, info) {
    console.error("UI error:", error, info);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="flex min-h-dvh items-center justify-center bg-background px-5 py-12">
        <div className="w-full max-w-lg rounded-2xl border border-border bg-card p-6 sm:p-8">
          <IconTile icon={TriangleAlert} tone="destructive" size="lg" />
          <h1 className="display mt-5 text-xl font-semibold">Something broke in the interface</h1>
          <p className="mt-2 text-base text-muted-foreground">
            Your backups and their history are stored by the server, not the browser, so
            nothing has been lost. Reloading should restore the page.
          </p>
          <pre className="mt-5 max-h-40 w-full overflow-auto scroll-thin rounded-xl border border-border bg-log-bg p-4 text-left font-mono text-[0.8125rem] whitespace-pre-wrap break-words">
            {String(this.state.error?.message || this.state.error)}
          </pre>
          <Button className="mt-6" onClick={() => window.location.reload()}>
            <RotateCw />
            Reload
          </Button>
        </div>
      </div>
    );
  }
}
