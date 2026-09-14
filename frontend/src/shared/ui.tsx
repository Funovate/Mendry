import { CircleAlert, LoaderCircle, RefreshCw } from "lucide-react";
import type { ReactNode } from "react";

export function IconButton({ label, children, onClick }: { label: string; children: ReactNode; onClick?: () => void }) {
  return <button className="icon-button" type="button" aria-label={label} title={label} onClick={onClick}>{children}</button>;
}

export function LoadingState({ label = "Loading" }: { label?: string }) {
  return <div className="app-state" role="status" aria-live="polite"><LoaderCircle className="spin" size={24} /><strong>{label}</strong></div>;
}

export function ErrorNotice({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return <div className="app-error" role="alert"><CircleAlert size={17} /><span>{message}</span>{onRetry && <button type="button" onClick={onRetry}><RefreshCw size={14} />Retry</button>}</div>;
}

export function PageError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="page-error-state" role="alert">
      <div className="page-error-card">
        <CircleAlert size={28} className="page-error-icon" aria-hidden="true" />
        <div className="page-error-content">
          <h2>Failed to load view</h2>
          <p>{message}</p>
        </div>
        <button className="primary-button" type="button" onClick={onRetry}>
          <RefreshCw size={14} aria-hidden="true" />
          Retry
        </button>
      </div>
    </div>
  );
}

export function StatusPill({ value }: { value: string }) {
  const tone = value === "Open" ? "open" : value === "Recovered" ? "recovered" : value === "Closed" ? "closed" : value === "P2" ? "p2" : "info";
  return <span className={`status-pill ${tone}`}>{value}</span>;
}
