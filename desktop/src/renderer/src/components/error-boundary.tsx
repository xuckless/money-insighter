import { Component, type ReactNode } from "react";

import { Button } from "@/components/ui/button";

// ErrorBoundary is the last line of defence for a page that throws while
// rendering; it shows the message and lets the user retry.
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error) {
    console.error(error);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="flex flex-col items-start gap-3 rounded-lg border bg-background p-6">
        <h2 className="text-lg font-semibold">Something went wrong</h2>
        <p className="text-sm text-muted-foreground">{this.state.error.message}</p>
        <Button variant="outline" onClick={() => this.setState({ error: null })}>
          Try again
        </Button>
      </div>
    );
  }
}
