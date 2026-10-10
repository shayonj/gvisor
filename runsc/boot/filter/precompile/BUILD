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
        "//runsc/boot/filter/config",
        "//runsc/flag",
    ],
)
