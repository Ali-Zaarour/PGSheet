// Rewrites dist/.gitkeep after a build.
//
// vite empties dist/ on every build, which deletes the placeholder that keeps
// the directory in the repository. main.go embeds dist/ with //go:embed, and
// that is resolved when Go compiles: wails generates bindings (a Go build)
// before it builds the frontend, so a fresh clone with no dist/ fails before
// it ever gets the chance to create one.
import { writeFileSync } from 'node:fs'

writeFileSync(
  new URL('../dist/.gitkeep', import.meta.url),
  `Keeps frontend/dist in the repository.

main.go embeds this directory with //go:embed, which is resolved when Go
compiles. wails generates bindings (a Go build) before it builds the
frontend, so on a fresh clone the directory has to exist already or the
build fails before it can create it.

vite empties this directory on each build, so npm run build writes this
file back afterwards. See frontend/package.json.
`,
)
