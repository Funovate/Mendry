import { useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Eye, EyeOff, LoaderCircle, Lock, User } from "lucide-react";
import brandMark from "../../assets/mendry-mark-reversed.svg";
import brandLogo from "../../assets/mendry-logo-horizontal.svg";
import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { api, messageFromError } from "../../api";
import { queryKeys } from "../../app/query";
import { useSessionQuery } from "../../app/queries";
import { ErrorNotice, LoadingState } from "../../shared/ui";

export function LoginPage() {
  const session = useSessionQuery();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  if (session.isPending) return <LoadingState label="Loading Mendry" />;
  if (session.data) return <Navigate to="/" replace />;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      const user = await api.login(username.trim(), password);
      queryClient.setQueryData(queryKeys.session, user);
      const destination = typeof location.state === "object" && location.state && "from" in location.state && typeof location.state.from === "string" ? location.state.from : "/";
      navigate(destination, { replace: true });
    } catch (requestError) {
      setError(messageFromError(requestError));
    } finally {
      setSubmitting(false);
      setPassword("");
    }
  };

  return (
    <main className="login-view">
      <aside className="login-story" aria-label="About Mendry">
        <div className="login-art" aria-hidden="true">
          <div className="login-orbit login-orbit-outer" />
          <div className="login-orbit login-orbit-inner" />
          <div className="login-mark"><img src={brandMark} alt="" /></div>
        </div>
        <div className="login-story-copy">
          <h2>From incident.<br />To insight.<br />To resolution.</h2>
        </div>
      </aside>
      <section className="login-entry" aria-labelledby="login-title">
        <div className="login-entry-top">
          <img className="brand-logo" src={brandLogo} alt="Mendry" width={156} height={39} />
        </div>
        <div className="login-panel">
          <div className="login-header">
            <h1 id="login-title">Sign in</h1>
            <p className="login-subtitle">Incident intelligence and automated remediation platform.</p>
          </div>
          <form onSubmit={submit} aria-busy={submitting}>
            <div className="login-field">
              <label htmlFor="login-username">Username</label>
              <div className="login-input-wrap">
                <User className="login-input-icon" size={16} aria-hidden="true" />
                <input
                  id="login-username"
                  aria-label="Username"
                  autoComplete="username"
                  placeholder="Enter your username"
                  value={username}
                  onChange={(event) => setUsername(event.target.value)}
                  required
                />
              </div>
            </div>
            <div className="login-field">
              <label htmlFor="login-password">Password</label>
              <div className="login-input-wrap">
                <Lock className="login-input-icon" size={16} aria-hidden="true" />
                <input
                  id="login-password"
                  aria-label="Password"
                  type={showPassword ? "text" : "password"}
                  autoComplete="current-password"
                  placeholder="Enter your password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  required
                />
                <button
                  type="button"
                  className="login-password-toggle"
                  onClick={() => setShowPassword((prev) => !prev)}
                  aria-label={showPassword ? "Hide secret text" : "Reveal secret text"}
                  title={showPassword ? "Hide" : "Show"}
                >
                  {showPassword ? <EyeOff size={16} aria-hidden="true" /> : <Eye size={16} aria-hidden="true" />}
                </button>
              </div>
            </div>
            {error && <ErrorNotice message={error} />}
            <button className="primary-button" type="submit" disabled={submitting || !username.trim() || !password}>
              {submitting ? <><LoaderCircle className="spin" size={17} />Signing in…</> : <>Sign in<ArrowRight className="login-submit-arrow" size={17} aria-hidden="true" /></>}
            </button>
          </form>
          <footer className="login-footer-meta">
            <span>Mendry SRE Core</span>
          </footer>
        </div>
      </section>
    </main>
  );
}
