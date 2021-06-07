let                                
   stable = import (builtins.fetchTarball {                                
      name = "nixos-20.09";                                
      url = "https://github.com/NixOS/nixpkgs/archive/20.09.tar.gz";                                
      sha256 = "1wg61h4gndm3vcprdcg7rc4s1v3jkm5xd7lw8r2f67w502y94gcy";                                
   }) {};                                

   nightly = import (builtins.fetchTarball {                                
     name = "nixos-nightly-2020-12-02";                                
     url = "https://github.com/NixOS/nixpkgs/archive/b6bca3d80619f1565ba0ea635b0d38234e41c6bd.tar.gz";                                
     sha256 = "09d4f6h98rmxnxzm1x07jxgrc81k6mz7fjigq375fkmb41j2kdsi";                                
   }) {};

   voodoo = import (builtins.fetchGit {                                
     url = "git@github.com:VoodooTeam/nix-pkgs.git";                                
     ref = "master";                                
   }) stable;

   unstable = import (builtins.fetchTarball https://nixos.org/channels/nixos-unstable/nixexprs.tar.xz) {};

  in                                

  stable.mkShell {                                
    buildInputs =
      [
        # go vim
         stable.go
         nightly.gopls
         nightly.asmfmt
         nightly.errcheck
         unstable.awscli2
        
         # operator-sdk cli
         voodoo.operator-sdk_1_3_0

         voodoo.helm_3_4_2

         # only for local testing
         stable.docker-compose
         voodoo.kind_0_9_0
         stable.awscli2
         stable.openssl
         stable.curl
         stable.jq
         stable.gnumake
         stable.cfssl
     ];                                
  }      


