{
  description = "desk: a Kanban board where every card is a Claude Code session";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      libs = pkgs: with pkgs; [ wayland libxkbcommon libGL vulkan-headers pipewire ];
      # releases are dated tags (2026.11.03, 2026.11.03.1); a flake build names its commit
      version = "dev-${self.shortRev or self.dirtyShortRev or "unknown"}";
    in
    {
      packages = forAll (pkgs: rec {
        desk = pkgs.buildGoModule {
          pname = "desk";
          inherit version;
          src = ./.;
          vendorHash = "sha256-ycBVx7YP2sOc8UR7LGXQakP8cO2mp4b0sHJA1YjezDc=";
          subPackages = [ "cmd/desk" ];
          tags = [ "nox11" ];
          env.CGO_CFLAGS_ALLOW = "-fno-strict-overflow";
          nativeBuildInputs = [ pkgs.pkg-config pkgs.makeWrapper ];
          buildInputs = libs pkgs;
          ldflags = [ "-s" "-w" "-X main.version=${version}" ];
          doCheck = false; # the tests start shells and terminals: run them with `go test` in the dev shell
          postInstall = ''
            install -Dm644 packaging/desk.desktop $out/share/applications/desk.desktop
            install -Dm644 packaging/desk.svg $out/share/icons/hicolor/scalable/apps/desk.svg
          '';
          # Off NixOS (Ubuntu, Fedora…) nixpkgs' libglvnd finds no GPU driver:
          # it only looks in /run/opengl-driver. There, use nixpkgs' Mesa
          # (Intel, AMD, nouveau; llvmpipe on anything else, e.g. NVIDIA's
          # own driver). A driver the user points at still wins.
          postFixup = ''
            wrapProgram $out/bin/desk --run '
            if [ ! -e /run/opengl-driver/lib ]; then
              export __EGL_VENDOR_LIBRARY_FILENAMES="''${__EGL_VENDOR_LIBRARY_FILENAMES:-${pkgs.mesa}/share/glvnd/egl_vendor.d/50_mesa.json}"
              export LIBGL_DRIVERS_PATH="''${LIBGL_DRIVERS_PATH:-${pkgs.mesa}/lib/dri}"
              export GBM_BACKENDS_PATH="''${GBM_BACKENDS_PATH:-${pkgs.mesa}/lib/gbm}"
            fi'
          '';
          meta = {
            description = "A Kanban board where every card is a Claude Code session";
            homepage = "https://github.com/lfdominguez/desk";
            license = pkgs.lib.licenses.mit;
            mainProgram = "desk";
            platforms = pkgs.lib.platforms.linux;
          };
        };
        default = desk;
      });

      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          # bashInteractive: stdenv's bash has no readline, and the infra tests' shells need it
          packages = [ pkgs.bashInteractive pkgs.go pkgs.pkg-config ] ++ libs pkgs;
          CGO_CFLAGS_ALLOW = "-fno-strict-overflow";
          GOTOOLCHAIN = "local";
          GOFLAGS = "-tags=nox11";
        };
      });
    };
}
