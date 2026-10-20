// Keep browser evidence in the selected run directory or this user's temp dir.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
function evidencePath(legacyPath) {
  if (!legacyPath.startsWith('/tmp/')) throw new Error('Expected a temporary evidence path');
  const root = process.env.UI_EVIDENCE_DIR || process.env.UI_SCREENSHOT_DIR || os.tmpdir();
  const relative = legacyPath.slice(5);
  const destination = path.resolve(root, relative);
  if (!destination.startsWith(path.resolve(root) + path.sep)) throw new Error('Evidence path escapes output directory');
  fs.mkdirSync(path.dirname(destination), {recursive: true});
  return legacyPath.endsWith('/') ? destination + path.sep : destination;
}
module.exports = {evidencePath};
