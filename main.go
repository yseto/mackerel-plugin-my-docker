package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"strings"

	dockerTypes "github.com/docker/docker/api/types"
	mp "github.com/mackerelio/go-mackerel-plugin"
)

const apiVersion = "v1.39"

type IdName struct {
	Id   string
	Name string
}

func getContainers(httpc *http.Client) ([]IdName, error) {
	response, err := httpc.Get("http://unix/" + apiVersion + "/containers/json")

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var jsonObj []dockerTypes.Container
	err = json.Unmarshal(body, &jsonObj)
	if err != nil {
		return nil, err
	}
	containers := make([]IdName, 0)

	for _, i := range jsonObj {
		containers = append(containers, IdName{
			Id: i.ID, Name: i.Names[0],
		})
	}

	return containers, nil
}

type Resource struct {
	Id            string
	ContainerName string
	CPUUsage      float64
	MemUsage      float64
}

func getStats(httpc *http.Client, container IdName) (*Resource, error) {
	response, err := httpc.Get("http://unix/" + apiVersion + "/containers/" + container.Id + "/stats?stream=false")

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var v *dockerTypes.Stats
	err = json.Unmarshal(body, &v)
	if err != nil {
		return nil, err
	}

	// https://docs.docker.com/engine/api/v1.39/#operation/ContainerStats
	usedMemory := v.MemoryStats.Usage - v.MemoryStats.Stats["cache"]
	// availableMemory := v.MemoryStats.Limit
	// float64(usedMemory) / float64(availableMemory) * 100.0,

	cpuDelta := v.CPUStats.CPUUsage.TotalUsage - v.PreCPUStats.CPUUsage.TotalUsage
	systemCpuDelta := v.CPUStats.SystemUsage - v.PreCPUStats.SystemUsage
	numberCpus := v.CPUStats.OnlineCPUs

	return &Resource{
		Id:            container.Id,
		ContainerName: container.Name,
		CPUUsage:      float64(cpuDelta) / float64(systemCpuDelta) * float64(numberCpus) * 100.0,
		MemUsage:      float64(usedMemory),
	}, nil
}

type MyDockerPlugin struct {
	Prefix string
	c      http.Client
}

func (n MyDockerPlugin) GraphDefinition() map[string]mp.Graphs {
	labelPrefix := strings.Title(n.MetricKeyPrefix())
	return map[string]mp.Graphs{
		"cpu.#": {
			Label: labelPrefix + " CPU",
			Unit:  mp.UnitPercentage,
			Metrics: []mp.Metrics{
				{Name: "Usage", Label: "Usage", Stacked: true},
			},
		},
		"memory.#": {
			Label: labelPrefix + " Memory",
			Unit:  mp.UnitInteger,
			Metrics: []mp.Metrics{
				{Name: "Usage", Label: "Usage", Stacked: true},
			},
		},
	}
}

func (n MyDockerPlugin) FetchMetrics() (map[string]float64, error) {
	containers, err := getContainers(&n.c)
	if err != nil {
		return nil, err
	}
	kv := make(map[string]float64)
	for _, i := range containers {
		res, err := getStats(&n.c, i)
		if err != nil {
			return nil, err
		}
		name := strings.Replace(res.ContainerName, "/", "", 1)
		kv["memory."+name+".Usage"] = res.MemUsage
		kv["cpu."+name+".Usage"] = res.CPUUsage
	}
	return kv, nil
}

func (n MyDockerPlugin) MetricKeyPrefix() string {
	if n.Prefix == "" {
		n.Prefix = "mydocker"
	}
	return n.Prefix
}

func main() {
	path := flag.String("socket", "/var/run/docker.sock", "docker-socket")
	optPrefix := flag.String("metric-key-prefix", "mydocker", "Metric key prefix")
	flag.Parse()

	httpc := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", *path)
			},
		},
	}

	n := MyDockerPlugin{
		Prefix: *optPrefix,
		c:      httpc,
	}
	plugin := mp.NewMackerelPlugin(n)
	plugin.Run()
}
