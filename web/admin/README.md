# LocalGate Admin

This directory contains the standalone LocalGate management frontend and its Go static-asset handler.

The browser application only communicates with `/api/admin/v1`; it does not depend on Go HTML templates. Production assets are embedded from `dist/` into the LocalGate binary. The current first implementation intentionally uses dependency-free HTML, CSS, and JavaScript so the server remains buildable without a Node dependency. A later frontend-only migration can introduce TypeScript or a component framework without changing the admin API.

Routes:

- `/admin`
- `/admin/history`
- `/admin/deployments`
- `/admin/approvals`

The UI loads without credentials and requests the configured HTTP Bearer token when an admin API call returns `401`. The token is kept in `sessionStorage` and attached only as an `Authorization` header.
