# deployments/ — the single source of truth for contract addresses

One JSON per network. **Every other file that needs an address reads or copies from here**:
`backend/.env.example`, `fly.*.toml.example` (CI fills `CHANGE_ME_*` from GitHub repository
variables, which must match these files), `contracts/script/e2e_*.sh`, the docs.

| File | Status |
|---|---|
| `coston2.json` | deployed — live at https://magicwebb.fly.dev |
| `songbird.json` | not deployed — config-flip ready |
| `flare.json` | not deployed — config-flip ready (mainnet: audit + multisig gate) |

Rules
- Never edit an address anywhere else first. Change it here, then propagate.
- `superseded` keeps old sets so historical commits and explorer links stay understandable.
- `tools/check-deployments.sh` validates the JSON against `schema.json` and greps the repo for
  stray addresses that are not in these files.
