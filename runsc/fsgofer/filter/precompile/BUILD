load("//tools:defs.bzl", "go_binary")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

go_binary(
    name = "precompile",
    srcs = ["main.go"],
    deps = [
        "//pkg/seccomp/precompiledseccomp",
        "//runsc/fsgofer/filter/config",
        "//runsc/flag",
    ],
)
