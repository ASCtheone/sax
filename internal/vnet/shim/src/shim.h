// Shared definitions for the sax vnet interpose shim (macOS).
#ifndef SAX_VNET_SHIM_H
#define SAX_VNET_SHIM_H

// DYLD_INTERPOSE rebinds every image's calls to _orig onto _repl, except calls
// made from inside this library, so hooks can call the original directly.
#define DYLD_INTERPOSE(_repl, _orig)                                        \
    __attribute__((used)) static struct {                                  \
        const void *repl;                                                   \
        const void *orig;                                                   \
    } _interpose_##_orig __attribute__((section("__DATA,__interpose"))) = { \
        (const void *)(unsigned long)&_repl, (const void *)(unsigned long)&_orig};

#define ENV_SHIM "SAX_VNET_SHIM"
#define ENV_ACTIVE "SAX_VNET_ACTIVE"
#define ENV_DYLD "DYLD_INSERT_LIBRARIES"

#endif
