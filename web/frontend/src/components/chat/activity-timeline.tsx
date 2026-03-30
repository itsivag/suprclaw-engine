import type { ActivityEventEnvelope, ActivityRunState } from "@/store/chat"

type StepStatus = "pending" | "in_progress" | "completed" | "failed"

interface TimelineStep {
  id: string
  title: string
  kind: string
  status: StepStatus
  details: string[]
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : ""
}

function getOrCreateStep(
  stepMap: Map<string, TimelineStep>,
  order: string[],
  stepId: string,
  title: string,
  kind: string,
): TimelineStep {
  const existing = stepMap.get(stepId)
  if (existing) {
    return existing
  }
  const created: TimelineStep = {
    id: stepId,
    title: title || "Step",
    kind: kind || "execution",
    status: "in_progress",
    details: [],
  }
  stepMap.set(stepId, created)
  order.push(stepId)
  return created
}

function buildTimelineSteps(events: ActivityEventEnvelope[]): TimelineStep[] {
  const stepMap = new Map<string, TimelineStep>()
  const order: string[] = []

  for (const event of events) {
    const eventData = event.data || {}
    const stepId = asString(eventData.step_id)

    switch (event.event_type) {
      case "step.started": {
        if (!stepId) break
        const step = getOrCreateStep(
          stepMap,
          order,
          stepId,
          asString(eventData.title),
          asString(eventData.kind),
        )
        step.status = "in_progress"
        break
      }
      case "step.completed": {
        if (!stepId) break
        const step = getOrCreateStep(
          stepMap,
          order,
          stepId,
          "Step",
          asString(eventData.kind),
        )
        step.status = "completed"
        const summary = asString(eventData.summary)
        if (summary) step.details.push(summary)
        break
      }
      case "step.failed": {
        if (!stepId) break
        const step = getOrCreateStep(
          stepMap,
          order,
          stepId,
          "Step",
          asString(eventData.kind),
        )
        step.status = "failed"
        const message = asString(eventData.message)
        if (message) step.details.push(message)
        break
      }
      case "reasoning.summary": {
        if (!stepId) break
        const step = getOrCreateStep(
          stepMap,
          order,
          stepId,
          "Reasoning",
          "reasoning",
        )
        const text = asString(eventData.text)
        if (text) step.details.push(text)
        break
      }
      case "tool.called": {
        if (!stepId) break
        const step = getOrCreateStep(
          stepMap,
          order,
          stepId,
          asString(eventData.display_name) ||
            `Using ${asString(eventData.tool_name)}`,
          "tool",
        )
        const preview = asString(eventData.arg_preview)
        if (preview) step.details.push(`args: ${preview}`)
        break
      }
      case "tool.completed": {
        if (!stepId) break
        const step = getOrCreateStep(stepMap, order, stepId, "Tool", "tool")
        const preview = asString(eventData.result_preview)
        if (preview) step.details.push(`result: ${preview}`)
        break
      }
      case "tool.failed": {
        if (!stepId) break
        const step = getOrCreateStep(stepMap, order, stepId, "Tool", "tool")
        step.status = "failed"
        const message = asString(eventData.message)
        if (message) step.details.push(`error: ${message}`)
        break
      }
      case "error.raised": {
        const syntheticID = `err-${event.event_id}`
        const step = getOrCreateStep(
          stepMap,
          order,
          syntheticID,
          "Error",
          "error",
        )
        step.status = "failed"
        const message = asString(eventData.message)
        if (message) step.details.push(message)
        break
      }
      default:
        break
    }
  }

  return order
    .map((stepID) => stepMap.get(stepID))
    .filter(Boolean) as TimelineStep[]
}

function statusLabel(status: StepStatus): string {
  if (status === "completed") return "done"
  if (status === "failed") return "failed"
  if (status === "pending") return "pending"
  return "working"
}

export function ActivityTimeline({ run }: { run?: ActivityRunState }) {
  if (!run || run.events.length === 0) {
    return null
  }

  const steps = buildTimelineSteps(run.events)

  return (
    <details open className="bg-card w-full rounded-xl border px-4 py-3">
      <summary className="text-muted-foreground cursor-pointer text-xs font-medium">
        Activity timeline
      </summary>
      <div className="mt-3 flex flex-col gap-3">
        {steps.map((step) => (
          <div key={step.id} className="rounded-md border px-3 py-2">
            <div className="flex items-center justify-between gap-3">
              <div className="text-sm font-medium">{step.title}</div>
              <div className="text-muted-foreground text-xs">
                {statusLabel(step.status)}
              </div>
            </div>
            {step.details.length > 0 && (
              <div className="text-muted-foreground mt-2 flex flex-col gap-1 text-xs">
                {step.details.map((detail, idx) => (
                  <p key={`${step.id}-${idx}`}>{detail}</p>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </details>
  )
}
