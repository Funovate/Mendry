import { describe, expect, it } from "vitest";
import { ApiError } from "../src/api";
import { createAppQueryClient, queryKeys } from "../src/app/query";

describe("project-scoped query state", () => {
  it("includes the stable project key in every project resource key", () => {
    expect(queryKeys.incidents("payments")).toEqual(["project", "payments", "incidents"]);
    expect(queryKeys.incidents("checkout")).not.toEqual(queryKeys.incidents("payments"));
    expect(queryKeys.configuration("payments")).toEqual(["project", "payments", "configuration"]);
  });

  it("clears project state when an authenticated query receives 401", async () => {
    const client = createAppQueryClient();
    client.setQueryData(queryKeys.session, { id: "user-1" });
    client.setQueryData(queryKeys.incidents("payments"), [{ id: "incident-1" }]);

    await expect(client.fetchQuery({
      queryKey: queryKeys.members("payments"),
      queryFn: () => Promise.reject(new ApiError(401, "unauthenticated", "Authentication is required.")),
    })).rejects.toBeInstanceOf(ApiError);

    expect(client.getQueryData(queryKeys.session)).toBeNull();
    expect(client.getQueryData(queryKeys.incidents("payments"))).toBeUndefined();
    expect(client.getQueryState(queryKeys.session)).toBeDefined();
  });
});
