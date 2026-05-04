---
description: Frontend style rules for the topbanana client
globs:
  - "internal/client/**"
---

## Styling conventions

Uses **Bulma** utility classes. Common patterns already in use:

| Element | Classes |
|---|---|
| Page wrapper | `section > .container` |
| Heading | `title`, `subtitle` |
| Button | `button is-primary` / `is-link` / `is-fullwidth` |
| Dropdown | `field > .control > .select > select` |
| Notification | `notification is-success` / `is-danger` |
| Progress bar | `progress is-primary` (with `:value` and `max="100"`) |
| Table | `table is-striped is-fullwidth` |

Do not add custom CSS unless Bulma has no equivalent. Inline styles are forbidden.

## What to avoid

- Do **not** introduce a build step (npm, Vite, webpack, etc.) — keep it build-free.
- Do **not** use TypeScript — plain `.js` only.
- Do **not** add a third-party component library or JS framework beyond Alpine + Bulma.
- Do **not** add reactive state outside the constructor (Alpine won't track it).
- Do **not** use inline styles — use Bulma classes.
- Do **not** hardcode base URLs — all fetch calls use relative paths.
- Do **not** add CSS files — add a `<style>` block to `index.html` only if Bulma truly has no equivalent.
