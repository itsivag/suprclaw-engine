import type { AgentInfo } from "@/store/chat"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

interface AgentSelectorProps {
  agents: AgentInfo[]
  activeAgentId: string
  onSelect: (agentId: string) => void
}

export function AgentSelector({ agents, activeAgentId, onSelect }: AgentSelectorProps) {
  return (
    <Select value={activeAgentId} onValueChange={onSelect}>
      <SelectTrigger
        size="sm"
        className="text-muted-foreground hover:text-foreground focus-visible:border-input h-8 max-w-[160px] min-w-[80px] bg-transparent shadow-none focus-visible:ring-0 sm:max-w-[220px]"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent position="popper" align="start">
        {agents.map((a) => (
          <SelectItem key={a.id} value={a.id}>
            {a.name || a.id}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
