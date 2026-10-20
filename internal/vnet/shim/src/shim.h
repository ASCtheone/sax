// Shared definitions for the sax vnet interpose shim (macOS).
#ifndef SAX_VNET_SHIM_H
#define SAX_VNET_SHIM_H

// Hooks are written once and bound per platform:
//   macOS: HOOK(bind) is sax_bind, rebound onto bind with DYLD_INTERPOSE;
//          calls from inside this library are not rebound, so REAL(bind)
//          is plain bind.
//   Linux: HOOK(bind) is bind itself (LD_PRELOAD puts it first in symbol
//          lookup) and REAL(bind) is the next definition, from dlsym.
#ifdef __APPLE__

#define DYLD_INTERPOSE(_repl, _orig)                                        \
    __attribute__((used)) static struct {                                  \
        const void *repl;                                                   \
        const void *orig;                                                   \
    } _interpose_##_orig __attribute__((section("__DATA,__interpose"))) = { \
        (const void *)(unsigned long)&_repl, (const void *)(unsigned long)&_orig};

#define HOOK(name) sax_##name
#define REAL(name) name
#define DECLARE_REAL(name)
#define EXPORT_HOOK(name) DYLD_INTERPOSE(sax_##name, name)
#define HOOK_LINKAGE static

#else

#include <dlfcn.h>

#define HOOK(name) name
#define REAL(name) (sax_real_##name())
#define DECLARE_REAL(name)                                                   \
    static __typeof__(&name) sax_real_##name(void) {                         \
        static __typeof__(&name) fn;                                         \
        if (!fn) fn = (__typeof__(&name))dlsym(RTLD_NEXT, #name);            \
        return fn;                                                           \
    }
#define EXPORT_HOOK(name)
#define HOOK_LINKAGE __attribute__((visibility("default")))

#endif

#define ENV_SHIM "SAX_VNET_SHIM"
#define ENV_ACTIVE "SAX_VNET_ACTIVE"
#define ENV_DYLD "DYLD_INSERT_LIBRARIES"

#endif
