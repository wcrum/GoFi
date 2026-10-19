package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/wcrum/gofi/desired"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/spec"
)

// runGet lists flows as a table, or prints named flows as YAML/JSON.
func runGet(ctx context.Context, g *globals, args []string) error {
	fs := newFlagSet("get", g)
	output := fs.String("o", "", "output: table (default), wide, yaml, json, name")
	fs.StringVar(output, "output", "", "output: table (default), wide, yaml, json, name")
	rest, err := parseInterspersed(fs, args, 0)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return errors.New("get requires a resource: flows, or flow NAME")
	}
	resource, names := rest[0], rest[1:]
	switch strings.ToLower(resource) {
	case "flows", "flow", "fl", "f":
	default:
		return fmt.Errorf("unknown resource %q (try: flows)", resource)
	}
	if *output == "" {
		*output = "table"
		if len(names) == 1 && resource != "flows" {
			*output = "yaml"
		}
	}
	client := g.client()
	defs, err := client.List(ctx)
	if err != nil {
		return fmt.Errorf("list flows: %s", describe(err))
	}
	if len(names) > 0 {
		byName := map[string]spec.FlowDefinition{}
		for _, def := range defs {
			byName[def.Name] = def
		}
		defs = defs[:0]
		for _, name := range names {
			def, ok := byName[name]
			if !ok {
				return fmt.Errorf("flow %q not found", name)
			}
			defs = append(defs, def)
		}
	} else {
		sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	}
	switch *output {
	case "yaml":
		return printYAML(defs)
	case "json":
		return printJSON(defs)
	case "name":
		for _, def := range defs {
			fmt.Printf("flow/%s\n", def.Name)
		}
		return nil
	case "table", "wide":
	default:
		return fmt.Errorf("unknown output %q", *output)
	}
	if len(defs) == 0 {
		fmt.Fprintln(os.Stderr, "No flows found.")
		return nil
	}
	statuses, err := client.ListStatus(ctx)
	if err != nil {
		return fmt.Errorf("list flow status: %s", describe(err))
	}
	fmt.Print(flowTable(defs, statuses, *output == "wide"))
	return nil
}

func printYAML(defs []spec.FlowDefinition) error {
	for i, def := range defs {
		data, err := desired.EncodeFlow(def)
		if err != nil {
			return err
		}
		if i > 0 {
			fmt.Println("---")
		}
		fmt.Print(string(data))
	}
	return nil
}

func printJSON(defs []spec.FlowDefinition) error {
	items := make([]string, 0, len(defs))
	for _, def := range defs {
		data, err := desired.EncodeFlow(def)
		if err != nil {
			return err
		}
		item, err := yaml.YAMLToJSON(data)
		if err != nil {
			return err
		}
		items = append(items, string(item))
	}
	if len(items) == 1 {
		fmt.Println(items[0])
		return nil
	}
	fmt.Printf("{\"apiVersion\":\"v1\",\"kind\":\"List\",\"items\":[%s]}\n", strings.Join(items, ","))
	return nil
}

// flowTable renders NAME SOURCE PORT STATE PHASE RESTARTS RUNTIME (wide adds
// SINKS, AGE, MESSAGE) from the definitions and their run status.
func flowTable(defs []spec.FlowDefinition, statuses []*gofiv1.FlowStatus, wide bool) string {
	byName := map[string]*gofiv1.FlowStatus{}
	for _, status := range statuses {
		byName[status.Name] = status
	}
	header := []string{"NAME", "SOURCE", "PORT", "STATE", "PHASE", "RESTARTS", "RUNTIME"}
	if wide {
		header = append(header, "SINKS", "AGE", "MESSAGE")
	}
	rows := make([][]string, 0, len(defs))
	for _, def := range defs {
		status := byName[def.Name]
		if status == nil {
			status = &gofiv1.FlowStatus{}
		}
		state := status.Desired
		if state == "" {
			state = def.State
		}
		if state == "" {
			state = "running"
		}
		row := []string{def.Name, sourceLabel(def.Source), portLabel(def.Source), state, dash(status.Phase), strconv.Itoa(int(status.Restarts)), dash(status.RuntimeStatus)}
		if wide {
			started := ""
			if status.StartedAt != nil {
				started = age(status.StartedAt.AsTime())
			}
			row = append(row, sinkLabel(def), dash(started), dash(status.Message))
		}
		rows = append(rows, row)
	}
	return table(header, rows)
}

// sourceLabel names the source kind and its transport, for example
// listen/tcp, fetch/http, subscribe/nats, python.
func sourceLabel(source spec.FlowSource) string {
	switch source.Kind() {
	case "listen":
		return "listen/" + dash(source.Listen.Network)
	case "fetch":
		switch {
		case source.Fetch.HTTP != nil:
			return "fetch/http"
		case source.Fetch.S3 != nil:
			return "fetch/s3"
		case source.Fetch.SQL != nil:
			return "fetch/sql"
		}
		return "fetch"
	case "subscribe":
		s := source.Subscribe
		switch {
		case s.NATS != nil:
			return "subscribe/nats"
		case s.MQTT != nil:
			return "subscribe/mqtt"
		case s.WebSocket != nil:
			return "subscribe/websocket"
		case s.SSE != nil:
			return "subscribe/sse"
		case s.Kafka != nil:
			return "subscribe/kafka"
		case s.AMQP != nil:
			return "subscribe/amqp"
		case s.JetStream != nil:
			return "subscribe/jetstream"
		case s.Redis != nil:
			return "subscribe/redis"
		case s.Zenoh != nil:
			return "subscribe/zenoh"
		}
		return "subscribe"
	case "python":
		return "python/" + dash(source.Python.Function)
	}
	return "-"
}

func portLabel(source spec.FlowSource) string {
	if source.Listen == nil {
		return "-"
	}
	return strconv.Itoa(source.Listen.Port)
}

func sinkLabel(def spec.FlowDefinition) string {
	if len(def.Sinks) == 0 {
		return "-"
	}
	types := make([]string, 0, len(def.Sinks))
	for _, sink := range def.Sinks {
		t := sink.Type
		if t == "" {
			t = "stdout"
		}
		types = append(types, t)
	}
	return strings.Join(types, ",")
}
