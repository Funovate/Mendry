# Implement: Git credential form by transport

1. Add Git-specific create fields to `RepositoryStep` and filter secrets by
   transport.
2. Keep `CredentialField` generic for source/webhook.
3. Add unit coverage for value composition and secret filtering if extracted.
4. Update `frontend/tests/application.spec.ts` Git credential interactions.
5. Update frontend component/state specs for the transport-specific form.
6. Run `npm run lint`, `typecheck`, `test`, `build`, and `test:e2e`.
