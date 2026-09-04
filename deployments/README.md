# deployments/ — the single source of truth for contract addresses

One JSON per network. **Every other file that needs an address reads or copies from here**:
`backend/.env.example`, `fly.*.toml.example` (CI fills `CHANGE_ME_*` directly from these
JSON files; the only repository variables are `<NETWORK>_ENABLED`), `contracts/script/e2e_*.sh`, the docs.

| File | Status |
|---|---|
| `coston2.json` | deployed — live at https://magicwebb.fly.dev |
| `songbird.json` | read-only — app live at https://magicwebb-songbird.fly.dev, contracts not deployed |
| `flare.json` | read-only — app live at https://magicwebb-flare.fly.dev, contracts not deployed |

Rules
- Never edit an address anywhere else first. Change it here, then propagate.
- `superseded` keeps old sets so historical commits and explorer links stay understandable.
- `tools/check-deployments.sh` validates the JSON against `schema.json` and greps the repo for
  stray addresses that are not in these files.
