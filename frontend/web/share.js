// Web entry point for the share affordance. The dialog logic lives in the
// shared source tree (frontend/shared/share.js) so the player client can inline
// the same code into its bundle instead of fetching it cross-tree at runtime
// (#721, slice 2). This entry re-exports the public surface and runs the
// shared module's DOMContentLoaded autowire as a side effect of the import, so
// the served bundle keeps the same behaviour and exports as the old hand-rolled
// file. esbuild bundles it to dist/share.js, served at /static/js/share.js.
export { openShareDialog, autowireShareTriggers } from '@shared/share.js';
