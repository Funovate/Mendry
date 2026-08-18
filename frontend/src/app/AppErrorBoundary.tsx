import { Component, type ReactNode } from "react";

type State = { error: Error | null };

export class AppErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  render() {
    if (!this.state.error) return this.props.children;
    return <main className="project-directory"><header><div><div className="eyebrow">Application</div><h1>Something went wrong</h1><p>The current screen could not be rendered.</p></div></header><section className="empty-projects"><button className="primary-button" type="button" onClick={() => window.location.assign("/")}>Reload application</button></section></main>;
  }
}
