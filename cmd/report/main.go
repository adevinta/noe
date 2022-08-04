package main

import (
	"context"
	"fmt"

	"github.com/adevinta/noe/pkg/registry"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

type usageRef struct {
	namespace string
	kind      string
	name      string
}

func (u usageRef) key() string {
	return fmt.Sprintf("%s/%s/%s", u.namespace, u.kind, u.name)
}

type imageUsage struct {
	platforms   []registry.Platform
	podCount    int
	totalCPU    resource.Quantity
	totalMemory resource.Quantity
	usageRefs   map[string]usageRef
	namespaces  map[string]struct{}
}

func platformsHasArm(platforms []registry.Platform) bool {
	for _, platform := range platforms {
		if platform.Architecture == "arm64" {
			return true
		}
	}
	return false
}

func main() {
	cfg := config.GetConfigOrDie()
	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		panic(nil)
	}
	pods := &v1.PodList{}
	err = cl.List(context.Background(), pods)
	if err != nil {
		panic(err)
	}
	knownImageArchs := map[string][]registry.Platform{}
	totalCPU := resource.Quantity{}
	totalMem := resource.Quantity{}

	podRunnableOnArm := map[string]*imageUsage{}
	for i, pod := range pods.Items {
		if false && i > 50 {
			break
		}
		// if len(runnableOnArm) > 3 {
		// 	break
		// }
		ref := usageRef{
			namespace: pod.Namespace,
			kind:      "Pod",
			name:      pod.Name,
		}
		if len(pod.OwnerReferences) > 0 {
			ref.kind = pod.OwnerReferences[0].Kind
			ref.name = pod.OwnerReferences[0].Name
		}
		for _, container := range pod.Spec.Containers {
			platforms, ok := knownImageArchs[container.Image]
			if !ok {
				platforms, err = registry.DefaultRegistry.ListArchs(context.Background(), "", container.Image)
				if err != nil {
					fmt.Println(container.Image, err)
					break
				}
				knownImageArchs[container.Image] = platforms
			}
			if !platformsHasArm(platforms) {
				break
			}

			usage, ok := podRunnableOnArm[container.Image]
			if !ok {
				usage = &imageUsage{
					platforms:  platforms,
					usageRefs:  map[string]usageRef{},
					namespaces: map[string]struct{}{},
				}
				podRunnableOnArm[container.Image] = usage
			}
			if container.Resources.Requests.Cpu() != nil {
				usage.totalCPU.Add(*container.Resources.Requests.Cpu())
			}
			if container.Resources.Requests.Memory() != nil {
				usage.totalMemory.Add(*container.Resources.Requests.Memory())
			}
			usage.usageRefs[ref.key()] = ref
			usage.namespaces[ref.namespace] = struct{}{}
		}
	}
	fmt.Println("image, namespace count", "total owners", "total pods", "total CPUs", "total Memory")
	for image, usage := range podRunnableOnArm {
		usage.podCount++
		usage.totalCPU.Add(usage.totalCPU)
		usage.totalMemory.Add(usage.totalMemory)
		totalCPU.Add(usage.totalCPU)
		totalMem.Add(usage.totalMemory)
		fmt.Printf("%s,%d,%d,%d,%s,%s\n", image, len(usage.namespaces), len(usage.usageRefs), usage.podCount, usage.totalCPU.String(), usage.totalMemory.String())
	}

	fmt.Println()
	fmt.Println("total,value")

	fmt.Println("cpu", totalCPU.String())
	fmt.Println("mem", totalMem.String())
}
