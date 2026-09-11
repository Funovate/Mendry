import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, messageFromError, type ListResult, type ProjectMember, type ProjectRole } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { ErrorNotice, LoadingState, PageError, StatusPill } from "../../shared/ui";

export function MembersPage() {
  const project = useCurrentProject();
  const queryClient = useQueryClient();
  const [username, setUsername] = useState("");
  const [role, setRole] = useState<ProjectRole>("viewer");
  const members = useQuery({
    queryKey: queryKeys.members(project.key),
    queryFn: ({ signal }) => api.listMembers(project.key, signal),
  });
  const upsertMember = useMutation({
    mutationFn: () => api.upsertMember(project.key, username.trim(), role),
    onSuccess: (member) => {
      queryClient.setQueryData<ListResult<ProjectMember>>(queryKeys.members(project.key), (current) => {
        const items = current?.items ?? [];
        const existed = items.some((item) => item.userId === member.userId);
        return {
          items: [...items.filter((item) => item.userId !== member.userId), member].sort((left, right) => left.username.localeCompare(right.username)),
          total: (current?.total ?? 0) + (existed ? 0 : 1),
        };
      });
      setUsername("");
    },
  });
  const deleteMember = useMutation({
    mutationFn: (member: ProjectMember) => api.deleteMember(project.key, member.username).then(() => member),
    onSuccess: (removed) => queryClient.setQueryData<ListResult<ProjectMember>>(queryKeys.members(project.key), (current) => {
      if (!current) return current;
      const existed = current.items.some((item) => item.userId === removed.userId);
      return {
        items: current.items.filter((item) => item.userId !== removed.userId),
        total: Math.max(0, current.total - (existed ? 1 : 0)),
      };
    }),
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    upsertMember.mutate();
  };

  if (members.isPending) return <LoadingState label="Loading members" />;
  if (members.isError) return <PageError message={messageFromError(members.error)} onRetry={() => void members.refetch()} />;
  const mutationError = upsertMember.error ?? deleteMember.error;

  return <section className="settings-view"><div className="view-header"><div><h1>Members</h1></div></div>{mutationError && <ErrorNotice message={messageFromError(mutationError)} />}{project.capabilities.manageMembers && <form className="member-form" onSubmit={submit}><label>Username<input aria-label="Member username" value={username} onChange={(event) => setUsername(event.target.value)} /></label><label>Project role<select aria-label="Member role" value={role} onChange={(event) => setRole(event.target.value as ProjectRole)}><option value="viewer">Viewer</option><option value="operator">Operator</option><option value="admin">Administrator</option></select></label><button className="primary-button" type="submit" disabled={upsertMember.isPending || !username.trim()}>{upsertMember.isPending ? <LoaderCircle className="spin" size={16} /> : <Plus size={16} />}Add or update</button></form>}<div className="table-wrap"><table><thead><tr><th>Username</th><th>Role</th><th>Can view</th><th>Can operate</th><th>Administration</th></tr></thead><tbody>{members.data.items.map((member) => <tr key={member.userId}><td>{member.username}</td><td><StatusPill value={member.role} /></td><td>Yes</td><td>{member.role === "viewer" ? "No" : "Yes"}</td><td>{member.role === "admin" ? "Members + configuration" : "No"}{project.capabilities.manageMembers && <button className="table-action" type="button" disabled={deleteMember.isPending} onClick={() => deleteMember.mutate(member)}>Remove</button>}</td></tr>)}</tbody></table>{members.data.items.length === 0 && <div className="table-empty">No members were returned for this project.</div>}</div></section>;
}
