#!/usr/bin/env bash
# board.sh — the ticket state baton, on the GitHub Project's Status field.
#
# State lives in the Status field of the "Top Banana" user Project, so the
# maintainer can drag a card (web or mobile) and Claude can set the same value
# from the CLI — one source of truth either way. `ready to merge` and
# `changes requested` are PR LABELS and are not managed here.
#
#   ./board.sh state <issue> "<Status>"   set an issue's state
#   ./board.sh get   <issue>              print an issue's state
#   ./board.sh list  "<Status>"           list issue numbers in that state
#   ./board.sh states                     print the valid state names
#
# Requires the `project` scope on the gh token.
set -euo pipefail

# The board is a USER project (github.com/users/starquake/projects/7), so every
# project query goes through `user(login:…)`, never `organization(login:…)`.
PROJECT_NUMBER=7
PROJECT_OWNER=starquake
PROJECT_ID="PVT_kwHOAA_wQM4Boo8v"
STATUS_FIELD_ID="PVTSSF_lAHOAA_wQM4Boo8vzhnIF04"

REPO_OWNER=starquake
REPO_NAME=topbanana

# Where `state` records its own writes for the board monitor to ignore
# (work-the-board). Losing it costs one spurious notification, never a missed
# maintainer move.
SELF_SET_FILE="${BOARD_SELF_SET_FILE:-${TMPDIR:-/tmp}/topbanana-board-selfset}"

# Option ids are looked up live by name: reordering a single-select's options
# replaces every option, minting new ids and clearing every item's value.
option_id() {
  local id
  id=$(gh api graphql -f query="{ user(login:\"$PROJECT_OWNER\"){ projectV2(number: $PROJECT_NUMBER){
        field(name:\"Status\"){ ... on ProjectV2SingleSelectField { options{ id name } } } } } }" \
      --jq ".data.user.projectV2.field.options[] | select(.name==\"$1\") | .id" 2>/dev/null)
  if [ -z "$id" ]; then
    echo "unknown state: $1" >&2
    echo "valid: $(gh api graphql -f query="{ user(login:\"$PROJECT_OWNER\"){ projectV2(number: $PROJECT_NUMBER){
          field(name:\"Status\"){ ... on ProjectV2SingleSelectField { options{ name } } } } } }" \
        --jq '[.data.user.projectV2.field.options[].name] | join(", ")')" >&2
    return 1
  fi
  printf '%s' "$id"
}

# Item id for an issue, adding the issue to the project if it is missing.
# Asks the ISSUE for its project items: `gh project item-list` is by far the
# most expensive GraphQL query here.
item_id() {
  local issue="$1" id
  id=$(gh api graphql -f query="{ repository(owner:\"$REPO_OWNER\", name:\"$REPO_NAME\"){
        issue(number: $issue){ projectItems(first:10){ nodes{ id project{ id } } } } } }" \
      --jq ".data.repository.issue.projectItems.nodes[] | select(.project.id==\"$PROJECT_ID\") | .id" 2>/dev/null | head -1)
  if [ -z "$id" ]; then
    id=$(gh project item-add "$PROJECT_NUMBER" --owner "$PROJECT_OWNER" \
          --url "https://github.com/$REPO_OWNER/$REPO_NAME/issues/$issue" \
          --format json --jq .id)
  fi
  printf '%s' "$id"
}

current_state() {
  gh api graphql -f query="{ repository(owner:\"$REPO_OWNER\", name:\"$REPO_NAME\"){
      issue(number: $1){ projectItems(first:10){ nodes{ project{ id }
        fieldValueByName(name:\"Status\"){ ... on ProjectV2ItemFieldSingleSelectValue { name } } } } } } }" \
    --jq ".data.repository.issue.projectItems.nodes[] | select(.project.id==\"$PROJECT_ID\") | .fieldValueByName.name // \"(unset)\""
}

case "${1:-}" in
  state)
    issue="$2"; want="$3"
    opt=$(option_id "$want")
    # A no-op move makes no transition, so its self-set entry would later
    # swallow a real maintainer move to the same state.
    if [ "$(current_state "$issue")" = "$want" ]; then
      echo "#$issue already $want"
      exit 0
    fi
    item=$(item_id "$issue")
    gh api graphql -f query="mutation{updateProjectV2ItemFieldValue(input:{
        projectId:\"$PROJECT_ID\", itemId:\"$item\", fieldId:\"$STATUS_FIELD_ID\",
        value:{singleSelectOptionId:\"$opt\"}}){projectV2Item{id}}}" >/dev/null
    # The monitor consumes this entry so it does not report our own move.
    printf '%s|%s\n' "$issue" "$want" >> "$SELF_SET_FILE"
    echo "#$issue -> $want"
    ;;
  get)
    current_state "$2"
    ;;
  list)
    # Paged: the board can hold more than one page of 100 items.
    gh api graphql --paginate -f query="query(\$endCursor: String) { user(login:\"$PROJECT_OWNER\"){ projectV2(number: $PROJECT_NUMBER){
        items(first:100, after:\$endCursor){ pageInfo{ hasNextPage endCursor } nodes{
        content{ ... on Issue { number } }
        fieldValueByName(name:\"Status\"){ ... on ProjectV2ItemFieldSingleSelectValue { name } } } } } } }" \
      --jq ".data.user.projectV2.items.nodes[] | select(.fieldValueByName.name==\"$2\") | .content.number // empty" | sort -n
    ;;
  states)
    gh api graphql -f query="{ user(login:\"$PROJECT_OWNER\"){ projectV2(number: $PROJECT_NUMBER){
        field(name:\"Status\"){ ... on ProjectV2SingleSelectField { options{ name } } } } } }" \
      --jq '.data.user.projectV2.field.options[].name'
    ;;
  *)
    sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
    exit 1
    ;;
esac
