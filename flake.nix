{
  description = "Development tools for the Logto Framework and Upjet providers";
  inputs.nixpkgs.url = "tarball+https://codeload.github.com/NixOS/nixpkgs/tar.gz/c59305bab2065cfecc4944690d9eedbb56f3a9fa";
  outputs = { self, nixpkgs }: let
    systems = [ "x86_64-linux" "aarch64-linux" ];
  in {
    packages = nixpkgs.lib.genAttrs systems (system: let pkgs = nixpkgs.legacyPackages.${system}; in {
      integration-tools = pkgs.symlinkJoin {
        name = "logto-integration-tools";
        paths = [ pkgs.kubernetes pkgs.etcd ];
      };
    });
    devShells = nixpkgs.lib.genAttrs systems (system: let pkgs = nixpkgs.legacyPackages.${system}; in {
      default = pkgs.mkShell {
        packages = [ pkgs.go pkgs.gotools pkgs.opentofu pkgs.gnumake pkgs.python3 pkgs.go-tools pkgs.govulncheck pkgs.actionlint pkgs.kubernetes pkgs.etcd pkgs.crane pkgs.cosign pkgs.crossplane-cli pkgs.syft pkgs.postgresql_17 ];
      };
    });
  };
}
