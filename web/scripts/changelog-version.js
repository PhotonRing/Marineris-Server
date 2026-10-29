// The newest released version in CHANGELOG.md, found with the same rule as
// .github/workflows/release.yml: the first "## [X.Y.Z] - date" heading.
// The static demo shows it (vite.config.js).

const heading = /^## \[([0-9][0-9.]*[0-9A-Za-z.-]*)\]/m;
const valid = /^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$/;

export function newestVersion(changelog) {
  const m = heading.exec(changelog);
  if (!m) throw new Error('CHANGELOG.md has no version section ("## [X.Y.Z] - date")');
  if (!valid.test(m[1])) throw new Error(`CHANGELOG.md: invalid version "${m[1]}"`);
  return m[1];
}
