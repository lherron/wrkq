#!/usr/bin/env node

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
  Tool,
} from "@modelcontextprotocol/sdk/types.js";
import { execFile } from "child_process";
import { promisify } from "util";
import { writeFile, unlink } from "fs/promises";
import { randomBytes } from "crypto";
import { join } from "path";

const execFileAsync = promisify(execFile);

// Temp directory for wrkq (per CLAUDE.md guidelines)
const TEMP_DIR = "/tmp/claude";

interface WrkqTaskWriteArgs {
  taskId: string;
  taskDescription: string;
}

/**
 * MCP Server for wrkq task management
 * Exposes wrkq CLI functionality as MCP tools
 */
class WrkqMCPServer {
  private server: Server;

  constructor() {
    this.server = new Server(
      {
        name: "wrkq-mcp-server",
        version: "0.1.0",
      },
      {
        capabilities: {
          tools: {},
        },
      }
    );

    this.setupToolHandlers();

    // Error handling
    this.server.onerror = (error) => {
      console.error("[MCP Error]", error);
    };

    process.on("SIGINT", async () => {
      await this.server.close();
      process.exit(0);
    });
  }

  private setupToolHandlers() {
    // List available tools
    this.server.setRequestHandler(ListToolsRequestSchema, async () => {
      return {
        tools: [
          {
            name: "wrkq_update_description",
            description:
              "Update a wrkq task's description content. Takes a task ID (friendly ID like T-00001, UUID, or path) and new description content. Uses wrkq CLI's apply command to update the task.",
            inputSchema: {
              type: "object",
              properties: {
                taskId: {
                  type: "string",
                  description:
                    "Task identifier: friendly ID (T-00001), UUID, or path (project/task-slug)",
                },
                taskDescription: {
                  type: "string",
                  description: "New description content for the task (markdown supported)",
                },
              },
              required: ["taskId", "taskDescription"],
            },
          } as Tool,
        ],
      };
    });

    // Handle tool calls
    this.server.setRequestHandler(CallToolRequestSchema, async (request) => {
      if (request.params.name === "wrkq_update_description") {
        const args = request.params.arguments as unknown as WrkqTaskWriteArgs;
        return await this.handleTaskWrite(args);
      }

      throw new Error(`Unknown tool: ${request.params.name}`);
    });
  }

  private async handleTaskWrite(args: WrkqTaskWriteArgs) {
    const { taskId, taskDescription } = args;

    if (!taskId || typeof taskId !== "string") {
      throw new Error("taskId is required and must be a string");
    }

    if (!taskDescription || typeof taskDescription !== "string") {
      throw new Error("taskDescription is required and must be a string");
    }

    // Generate unique temp file name
    const tempFileName = `wrkq-task-${randomBytes(8).toString("hex")}.md`;
    const tempFilePath = join(TEMP_DIR, tempFileName);

    try {
      // Write task description to temp file
      await writeFile(tempFilePath, taskDescription, "utf8");

      // Call wrkq apply command to update the task
      // The apply command syntax: wrkq apply <taskId> <file> [--format md]
      // By default, wrkq apply updates only the description (body)
      // Using --format md allows plain markdown without front matter
      const { stdout, stderr } = await execFileAsync("wrkq", [
        "apply",
        taskId,
        tempFilePath,
        "--format",
        "md",
      ]);

      // Clean up temp file
      await unlink(tempFilePath);

      return {
        content: [
          {
            type: "text",
            text: `Successfully updated task ${taskId}\n\nOutput:\n${stdout}${stderr ? `\nWarnings:\n${stderr}` : ""}`,
          },
        ],
      };
    } catch (error: any) {
      // Clean up temp file on error
      try {
        await unlink(tempFilePath);
      } catch {
        // Ignore cleanup errors
      }

      const errorMessage =
        error.code === "ENOENT"
          ? "wrkq command not found. Please ensure wrkq is installed and in PATH."
          : error.stderr || error.message;

      throw new Error(`Failed to update task: ${errorMessage}`);
    }
  }

  async run() {
    const transport = new StdioServerTransport();
    await this.server.connect(transport);
    console.error("wrkq MCP server running on stdio");
  }
}

// Start the server
const server = new WrkqMCPServer();
server.run().catch((error) => {
  console.error("Fatal error running server:", error);
  process.exit(1);
});
