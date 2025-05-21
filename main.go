package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func main() {
	// CLI flags
	broker := flag.String("broker", "tcp://localhost:1883", "MQTT broker URI")
	prefix := flag.String("topic", "markdown", "Topic prefix for publishing/reconstructing")
	mode := flag.String("mode", "publish", "Mode: 'publish' or 'reconstruct'")
	duration := flag.Int("duration", 10, "Reconstruct mode: duration in seconds to collect messages")
	output := flag.String("output", "", "Reconstruct mode: output file for reconstructed Markdown")

	// Parse flags before using them
	flag.Parse()

	// Validate mode
	if *mode != "publish" && *mode != "reconstruct" {
		fmt.Println("Error: mode must be either 'publish' or 'reconstruct'")
		os.Exit(1)
	}

	// Setup MQTT client
	opts := mqtt.NewClientOptions().AddBroker(*broker).SetClientID("md-tool")
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("MQTT connect error: %v", token.Error())
	}
	defer client.Disconnect(250)

	switch *mode {
	case "publish":
		if flag.NArg() < 1 {
			fmt.Println("Usage: go run main.go -mode publish [flags] <markdown-file>")
			os.Exit(1)
		}
		mdPath := flag.Arg(0)
		data, err := os.ReadFile(mdPath)
		if err != nil {
			log.Fatalf("Failed to read %s: %v", mdPath, err)
		}
		// Parse and publish
		parser := goldmark.DefaultParser()
		root := parser.Parse(text.NewReader(data))
		var stack []string
		ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			switch node := n.(type) {
			case *ast.Heading:
				if entering {
					lvl := node.Level
					if len(stack) >= lvl {
						stack = stack[:lvl-1]
					}
					var txt []byte
					for c := node.FirstChild(); c != nil; c = c.NextSibling() {
						if textNode, ok := c.(*ast.Text); ok {
							txt = append(txt, textNode.Segment.Value(data)...)
						}
					}
					title := strings.ReplaceAll(string(txt), " ", "_")
					stack = append(stack, title)
				}
			case *ast.Paragraph:
				if entering {
					var txt []byte
					for c := node.FirstChild(); c != nil; c = c.NextSibling() {
						if textNode, ok := c.(*ast.Text); ok {
							txt = append(txt, textNode.Segment.Value(data)...)
						}
					}
					content := strings.TrimSpace(string(txt))
					if content != "" && len(stack) > 0 {
						topic := *prefix + "/" + strings.Join(stack, "/")
						token := client.Publish(topic, 0, true, content)
						token.Wait()
						log.Printf("Published to %s: %s", topic, content)
					}
				}
			}
			return ast.WalkContinue, nil
		})

	case "reconstruct":
		msgs := make(map[string]string)
		handler := func(c mqtt.Client, msg mqtt.Message) {
			topic := strings.TrimPrefix(msg.Topic(), *prefix+"/")
			msgs[topic] = string(msg.Payload())
		}
		if token := client.Subscribe(*prefix+"/#", 0, handler); token.Wait() && token.Error() != nil {
			log.Fatalf("Subscribe error: %v", token.Error())
		}
		log.Printf("Collecting messages for %d seconds...", *duration)
		// Wait for duration or interruption
		done := make(chan os.Signal, 1)
		signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
		timer := time.NewTimer(time.Duration(*duration) * time.Second)
		select {
		case <-timer.C:
		case <-done:
			log.Println("Interrupted, reconstructing...")
		}
		client.Unsubscribe(*prefix + "/#")

		// Build and write Markdown
		var lines []string
		type entry struct {
			parts   []string
			content string
		}
		var entries []entry
		for t, content := range msgs {
			parts := strings.Split(t, "/")
			entries = append(entries, entry{parts, content})
		}
		sort.Slice(entries, func(i, j int) bool {
			return strings.Join(entries[i].parts, "/") < strings.Join(entries[j].parts, "/")
		})

		var last []string
		for _, e := range entries {
			for i, part := range e.parts {
				if i >= len(last) || last[i] != part {
					lines = append(lines, fmt.Sprintf("%s %s", strings.Repeat("#", i+1), strings.ReplaceAll(part, "_", " ")))
					lines = append(lines, "")
					if i < len(last) {
						last = last[:i]
					}
					last = append(last, part)
				}
			}
			lines = append(lines, e.content, "")
		}

		outputData := strings.Join(lines, "\n")
		if *output != "" {
			if err := os.WriteFile(*output, []byte(outputData), 0644); err != nil {
				log.Fatalf("Failed to write to %s: %v", *output, err)
			}
			log.Printf("Reconstructed Markdown written to %s", *output)
		} else {
			fmt.Print(outputData)
		}

	default:
		fmt.Println("Unknown mode:", *mode)
		os.Exit(1)
	}
}
