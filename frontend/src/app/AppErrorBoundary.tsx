import { Component, type ReactNode } from "react";
import brandLogo from "../assets/mendry-logo-horizontal.svg";

type State = { error: Error | null };

export class AppErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  render() {
    if (!this.state.error) return this.props.children;
    const error = this.state.error;

    return (
      <main className="project-directory">
        <div className="project-directory-panel error-boundary-panel">
          <div className="directory-header-top">
            <img className="brand-logo directory-logo" src={brandLogo} alt="Mendry" width={146} height={36} />
          </div>
          <header>
            <h1>Application error</h1>
            <p className="project-directory-subtitle">
              The application encountered an unexpected runtime error and could not render the current screen.
            </p>
          </header>
          <div className="error-boundary-details">
            <p className="error-message">
              <strong>{error.name || "Error"}:</strong> {error.message || "Unknown error"}
            </p>
            {error.stack && (
              <details className="error-stack-details">
                <summary>Stack trace</summary>
                <pre>{error.stack}</pre>
              </details>
            )}
          </div>
          <div className="error-boundary-actions">
            <button className="primary-button" type="button" onClick={() => window.location.assign("/")}>
              Reload application
            </button>
            <a className="secondary-button" href="/">
              Return home
            </a>
          </div>
        </div>
      </main>
    );
  }
}
