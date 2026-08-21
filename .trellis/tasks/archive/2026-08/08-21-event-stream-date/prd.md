# Show dates in event stream timestamps

## Goal

Let operators distinguish Event stream entries from different calendar days without opening any additional detail.

## Background

- The production Event stream renders each observation timestamp with `formatTime`, which currently shows only local hours, minutes, and seconds (`frontend/src/features/observations/ObservationsPage.tsx:16`).
- Observation timestamps are ISO strings supplied by the existing API and are already parsed in the browser (`frontend/src/shared/format.ts:7`).
- The existing route-mocked Event stream fixture uses `2026-08-13T07:59:00Z` (`frontend/tests/application.spec.ts:269`).

## Requirements

- Display the observation's local calendar date together with its local time in every Event stream row.
- Include year, month, day, hour, minute, and second so the value is unambiguous across days and years.
- Preserve the current fallback behavior for invalid timestamp strings by displaying the original value.
- Keep the change scoped to the production Event stream; other date displays and the prototype screen are out of scope.

## Acceptance Criteria

- [x] A valid Event stream observation timestamp renders a localized value containing its year, month, day, hour, minute, and second.
- [x] The observation message, level, and service/source label continue to render unchanged.
- [x] An automated test verifies that the Event stream timestamp includes the date and time derived from the API fixture.
- [x] Frontend lint, type-check, relevant tests, build, and diff checks pass.

## Out Of Scope

- Changing the API timestamp contract or storage format.
- Changing timestamps on Incidents, Audit, or prototype-only screens.
- Adding timezone selection or forcing a fixed timezone.
