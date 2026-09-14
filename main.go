package main

import (
	"context"
	"log"
	"os"
	"runtime/pprof"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/DigiBugCat/terraform-provider-obs/tfprovider"
)

func main() {
	// OBS_PROVIDER_CPUPROFILE=<path> writes a CPU profile of this plugin process for the
	// lifetime of one Terraform command. Diagnostic only; off unless set.
	if p := os.Getenv("OBS_PROVIDER_CPUPROFILE"); p != "" {
		if f, err := os.Create(p); err == nil {
			if err := pprof.StartCPUProfile(f); err == nil {
				defer pprof.StopCPUProfile()
			}
		}
	}
	err := providerserver.Serve(context.Background(), tfprovider.New, providerserver.ServeOpts{
		Address: "registry.terraform.io/digibugcat/obs",
	})
	if err != nil {
		log.Fatal(err)
	}
}
