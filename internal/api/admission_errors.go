package api

// An admission error or failed receipt read does not establish whether the
// original operation committed. Describe the existing recovery contract without
// promising recovery, non-acceptance or any billing outcome.
const generationAcceptanceUnconfirmed = "Generation acceptance could not be confirmed. Check the Journal or the original generation's status before creating another generation. To retry the same operation, preserve its original Idempotency-Key, endpoint and request body in the same account and workspace. If no key was sent or verification keeps failing, ask an operator to check admission and queue state before submitting again."
