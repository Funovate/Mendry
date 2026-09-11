import { useQueryClient } from "@tanstack/react-query";
import { Gauge, LoaderCircle, LockKeyhole } from "lucide-react";
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

  return <main className="login-view"><section className="login-panel"><div className="login-brand"><span><Gauge size={23} /></span><strong>Mendry</strong></div><div><div className="eyebrow">Incident operations</div><h1>Sign in</h1><p>Projects, collection settings, events, and incident actions are scoped to your account.</p></div><form onSubmit={submit}><label>Username<input aria-label="Username" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} autoFocus /></label><label>Password<input aria-label="Password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} /></label>{error && <ErrorNotice message={error} />}<button className="primary-button" type="submit" disabled={submitting || !username.trim() || !password}>{submitting ? <LoaderCircle className="spin" size={16} /> : <LockKeyhole size={16} />}Sign in</button></form></section></main>;
}
