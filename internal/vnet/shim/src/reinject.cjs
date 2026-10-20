// sax vnet: Node fallback for when the interpose shim was lost on the way in
// (e.g. started through "#!/usr/bin/env node" by a SIP-protected shell).
// Loaded via NODE_OPTIONS=--require; if the shim is not active in this very
// process, re-exec node in place with DYLD_INSERT_LIBRARIES set. execve keeps
// the pid, stdio and argv, so callers never notice.
'use strict';

const env = process.env;
const pid = String(process.pid);
const shim = env.SAX_VNET_SHIM;

function mainThread() {
  try {
    return require('worker_threads').isMainThread;
  } catch {
    return true;
  }
}

if (shim && env.SAX_VNET_ACTIVE !== pid && env.SAX_VNET_REEXEC !== pid &&
    typeof process.execve === 'function' && mainThread()) {
  const emit = process.emitWarning;
  process.emitWarning = () => {}; // process.execve is experimental
  try {
    process.execve(
      process.execPath,
      [process.argv0, ...process.execArgv, ...process.argv.slice(1)],
      { ...env, DYLD_INSERT_LIBRARIES: shim, SAX_VNET_REEXEC: pid },
    );
  } catch {
    // Fall through: run without the shim rather than fail the program.
  } finally {
    process.emitWarning = emit;
  }
}
