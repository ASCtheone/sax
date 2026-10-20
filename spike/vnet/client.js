// Fetches each URL and prints the body or the error code.
(async () => {
  for (const url of process.argv.slice(2)) {
    try {
      const body = await (await fetch(url)).text();
      console.log(`  ${url.padEnd(34)} -> ${body.trim().slice(0, 40)}`);
    } catch (e) {
      console.log(`  ${url.padEnd(34)} -> ERR ${e.cause?.code ?? e.message}`);
    }
  }
})();
