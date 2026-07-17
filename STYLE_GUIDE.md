# Navidrome (Modified) — Styling Guide

Conventions for this fork of Navidrome, derived from the upstream codebase and the
local modifications (YouTube import pipeline, delete actions, extra themes). Follow
these when adding or changing code so new work blends in with what's already here.

## Formatting is tool-enforced — don't hand-format

| Area | Tool | Command |
|------|------|---------|
| JS/JSX/TS | Prettier 3 (`ui/prettier.config.js`) | `cd ui && npm run prettier` |
| JS/JSX/TS lint | ESLint, zero warnings allowed | `cd ui && npm run lint` |
| Go | gofmt + golangci-lint (`.golangci.yml`) | `make format` / `make lint` |

Prettier settings: **single quotes, no semicolons, always parenthesize arrow args**.
Never fight these; run the formatter instead.

---

## Frontend (ui/)

### Stack — use what's here, not what's newest

- React **17** with react-admin **3.x** and Material-UI **v4** (`@material-ui/core`).
- Styling is **JSS via `makeStyles`**. Do **not** use MUI v5 `sx` props,
  styled-components, Tailwind, or CSS modules — none of them exist in this codebase.
- Tests run under **Vitest** (`npm test`).

### File & naming conventions

- Feature folders under `ui/src/` (`album/`, `song/`, `youtube/`, `playlist/`, …).
  New features get their own folder (e.g. `ui/src/youtube/`).
- Components: `PascalCase.jsx`, one component per file, default export at the bottom:
  `const DeleteAlbumButton = (...) => {...}` … `export default DeleteAlbumButton`.
- Hooks and plain modules: `camelCase.js` (`useCurrentTheme.js`, `youtubeApi.js`).
- API wrappers live next to the feature (`youtube/youtubeApi.js`), shared hooks in
  `common/` (e.g. `useInterval`).

### Component styling pattern

Define styles at the top of the file with `makeStyles`, consume with a `classes`
object:

```jsx
import { makeStyles, alpha } from '@material-ui/core/styles'
import clsx from 'clsx'

const useStyles = makeStyles((theme) => ({
  root: { marginTop: '1em', maxWidth: 700 },
  actions: {
    display: 'flex',
    gap: theme.spacing(2),
    marginTop: theme.spacing(3),
  },
  deleteButton: {
    color: theme.palette.error.main,
    '&:hover': { backgroundColor: alpha(theme.palette.error.main, 0.12) },
  },
}))

const MyComponent = ({ className }) => {
  const classes = useStyles()
  return <div className={clsx(classes.root, className)}>…</div>
}
```

Rules of thumb:

- Spacing comes from `theme.spacing(n)`; colors from `theme.palette.*` — hardcoded
  hex values belong in theme files only.
- Combine class names with `clsx`, and accept/forward a `className` prop on
  reusable components.
- Pass a `{ name: 'Ra…' / 'ND…' }` second argument to `makeStyles` only when themes
  need to target the component via `overrides`.
- Layout is flexbox (`display: 'flex'` + `gap`), not floats or grids of `<Grid>`.

### react-admin integration

- Use react-admin hooks — `useTranslate`, `useNotify`, `useRedirect`,
  `useDataProvider`, controllers like `useDeleteWithConfirmController` — rather than
  reimplementing their behavior.
- **Every user-visible string goes through `translate()`** with a key in
  `ui/src/i18n/en.json`. Resource-specific keys live under
  `resources.<resource>.…`, generic actions reuse `ra.action.*`.
- Notifications: `notify('resources.album.notifications.deleted', 'info', {...})`;
  errors as `notify(msg, { type: 'warning' })`.
- New pages are wired in `ui/src/routes.jsx` and get a menu entry in
  `ui/src/layout/Menu.jsx`.

### Themes (`ui/src/themes/`)

Upstream docs: https://www.navidrome.org/docs/developers/creating-themes/

- One file per theme, default-exporting a Material-UI **v4** theme object:
  `themeName` (display name), `typography`, `palette` (with `type: 'dark' | 'light'`),
  and `overrides` keyed by MUI component (`MuiButton`, `MuiMenuItem`, …).
- Define the palette as named constants at the top (e.g. `const spotifyGreen = {...}`)
  and reference them, instead of repeating hex literals.
- Player styling can't be themed through MUI: put raw CSS for
  `react-jinke-music-player` in a sibling `<name>.css.js` exporting a template-string
  `stylesheet`, and attach it as `player: { stylesheet }`. `useCurrentTheme` injects
  it as a `<style>` override at runtime.
- The `musicListActions` block (album/playlist action-button styling with the scaled
  round primary button) is a copy-adapt pattern shared across themes — start from an
  existing theme close to yours.
- Register new themes in `themes/index.js` **in alphabetical order** (the file says
  so), after the two classic defaults.

---

## Backend (Go)

- Custom features are self-contained packages under `core/<feature>/`
  (`core/ytimport`, `core/ytdlp`, `core/onetagger`), exposed over HTTP from
  `server/nativeapi/`. Wiring goes through Wire (`core/wire_providers.go`,
  regenerate with `make wire`).
- Start each package with a real doc comment explaining what it does and how the
  pieces flow (see `core/ytimport/ytimport.go` for the expected level of detail).
  Comments explain constraints and *why* (e.g. why a path must match
  docker-compose), not what the next line does.
- Errors: sentinel values as `var ErrJobNotFound = errors.New("job not found")`;
  wrap with `fmt.Errorf("…: %w", err)`; compare with `errors.Is`.
- Enumerations as typed string constants
  (`type State string` + `StateDownloading State = "downloading"`).
- Logging via the project's `log` package (`github.com/navidrome/navidrome/log`),
  never `fmt.Print*` (forbidigo will flag it).
- Config options go in `conf/configuration.go` following the existing viper pattern.
- Tests use **Ginkgo v2 + Gomega** suites (`*_test.go` alongside the code, one
  `_suite_test.go` per package). Run with `make test`.

---

## Commits

From `CONTRIBUTING.md`: `<type>(scope): <description>` where type is one of
`feat fix sec docs style refactor perf test build revert chore`, e.g.
`feat(ui): add YouTube download page`. Keep descriptions imperative and lowercase.
