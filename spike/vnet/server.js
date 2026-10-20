// Same app, same port in every router: replies with its identity.
const http = require('http');
const [name, version, port] = process.argv.slice(2);
http
  .createServer((req, res) => res.end(`${name} ${version} ${req.url}\n`))
  .listen(Number(port), function () {
    const a = this.address();
    console.log(`[${name} ${version}] listening ${a.address}:${a.port}`);
  });
