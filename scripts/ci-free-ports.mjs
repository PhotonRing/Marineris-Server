// Prints free loopback ports for the test servers, as NAME=port lines for
// $GITHUB_ENV. Several runners can share one machine: fixed ports would
// collide between jobs running at the same time.
//
//   node scripts/ci-free-ports.mjs E2E_EDGE_PORT E2E_LOCAL_PORT >> "$GITHUB_ENV"
import net from 'node:net';

const names = process.argv.slice(2);
// All sockets stay open until every port is known, so no port comes twice.
const servers = await Promise.all(
  names.map(
    () =>
      new Promise((resolve, reject) => {
        const s = net.createServer();
        s.once('error', reject);
        s.listen(0, '127.0.0.1', () => resolve(s));
      }),
  ),
);
names.forEach((name, i) => console.log(`${name}=${servers[i].address().port}`));
servers.forEach((s) => s.close());
