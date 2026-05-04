// napi-build emits the shims a Node native addon needs at compile
// time (Windows .lib stubs against node.exe, exported symbol list,
// etc.). Doing it here keeps the Cargo.toml free of platform forks.
fn main() {
    napi_build::setup();
}
