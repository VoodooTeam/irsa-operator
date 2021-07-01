let
  pkgs = import
    (builtins.fetchTarball {
      name = "nixos-21.05";
      url = "https://github.com/NixOS/nixpkgs/archive/21.05.tar.gz";
      sha256 = "1ckzhh24mgz6jd1xhfgx0i9mijk6xjqxwsshnvq789xsavrmsc36";
    })
    { };

  voodoo = import
    (builtins.fetchGit {
      url = "git@github.com:VoodooTeam/devops-nix-pkgs.git";
      ref = "v0.1.0";
    })
    { inherit pkgs; system = builtins.currentSystem; };

  mach-nix = import
    (builtins.fetchGit {
      url = "https://github.com/DavHau/mach-nix/";
      ref = "refs/tags/3.1.1";
    })
    {
      python = "python39";
      inherit pkgs;
    };

  pythonPkgs = mach-nix.mkPython {
    requirements = ''
      chaostoolkit
      chaostoolkit-kubernetes
      jsonpath2
    '';
  };
in
pkgs.mkShell {
  buildInputs =
    [
      # go vim
      pkgs.go
      pkgs.gopls
      pkgs.asmfmt
      pkgs.errcheck

      # operator-sdk cli
      voodoo.operator-sdk_1_3_0
      voodoo.helm_3_4_2

      # only for local testing
      pkgs.docker-compose
      voodoo.kind_0_9_0
      pkgs.awscli2
      pkgs.openssl
      pkgs.curl
      pkgs.jq
      pkgs.gnumake
      pkgs.envsubst

      pythonPkgs
    ];
}
