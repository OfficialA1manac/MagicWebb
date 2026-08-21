#!/usr/bin/env bash
# Validates deployments/*.json and fails if any 0x…40 address that looks like a
# MagicWebb contract appears elsewhere in the repo without being listed in
# deployments/ (current or superseded). Run in CI and via `make check-deployments`.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

node -e '
const fs=require("fs");
const files=fs.readdirSync("deployments").filter(f=>/^(coston2|songbird|flare)\.json$/.test(f));
const known=new Set();
for(const f of files){
  const d=JSON.parse(fs.readFileSync("deployments/"+f,"utf8"));
  if(!["deployed","not-deployed"].includes(d.status)) throw new Error(f+": bad status");
  for(const [k,v] of Object.entries(d.contracts)){
    if(d.status==="deployed" && !/^0x[0-9a-fA-F]{40}$/.test(v||"")) throw new Error(f+": "+k+" missing");
    if(d.status==="not-deployed" && v!==null) throw new Error(f+": "+k+" must be null when not deployed");
    if(v) known.add(v.toLowerCase());
  }
  for(const s of d.superseded||[]) for(const [k,v] of Object.entries(s)) if(/^0x/.test(v)) known.add(v.toLowerCase());
  for(const c of d.trackedCollections||[]) known.add(c.toLowerCase());
}
fs.writeFileSync(".deployments-known.tmp",[...known].join("\n"));
console.log("deployments: "+files.length+" files, "+known.size+" known addresses");
'

# Addresses referenced in config-ish files outside deployments/.
stray=0
while IFS= read -r line; do
  addr=$(echo "$line" | grep -oE '0x[0-9a-fA-F]{40}' | head -1 | tr 'A-F' 'a-f')
  if ! grep -qix "$addr" .deployments-known.tmp; then
    echo "STRAY ADDRESS not in deployments/: $line"; stray=1
  fi
done < <(git grep -nE '(MARKETPLACE|AUCTION|OFFERBOOK|NFT|MARKETPLACE_MANAGER)_ADDR[= ]+"?0x[0-9a-fA-F]{40}|^(MP|AH|OB)=0x[0-9a-fA-F]{40}' -- ':!deployments' ':!CHANGELOG.md' ':!contracts/lib' || true)
rm -f .deployments-known.tmp
[ "$stray" = 0 ] && echo "check-deployments: OK"
exit $stray
