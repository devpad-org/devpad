import { computed, ref, shallowRef, watch, type Ref } from 'vue'
import { pipelineApi, type JobNote } from '@/api/pipelines'
import { LogLines, type LogLine } from '@/utils/logLines'
import { usePolling } from './usePolling'

const POLL_INTERVAL_MS = 1500

export interface JobOutputTarget {
  projectId: number
  pipelineId: number
  jobId: number
}

// useJobOutput follows a pipeline job's output while active: it reads from
// the start of what is kept, then asks each time from where the last
// answer said to go on, until the job is done. Devpad's notes on the job
// come apart from the output, whole on every answer.
export function useJobOutput(target: Ref<JobOutputTarget | null>, active: Ref<boolean>) {
  const buffer = new LogLines()
  const lines = shallowRef<LogLine[]>(buffer.lines)
  const notes = ref<JobNote[]>([])
  const done = ref(false)
  const loaded = ref(false)
  const error = ref('')
  // The output starts after the first byte the job printed: only its end
  // is kept.
  const startsLate = ref(false)
  // Output came faster than it was read, and some in between was lost.
  const skipped = ref(false)
  const droppedLines = ref(0)
  let next = 0
  // Bumped whenever the job changes, so a slow answer for the last one is
  // dropped.
  let generation = 0

  function reset() {
    generation++
    buffer.clear()
    lines.value = []
    notes.value = []
    done.value = false
    loaded.value = false
    error.value = ''
    startsLate.value = false
    skipped.value = false
    droppedLines.value = 0
    next = 0
  }

  async function read() {
    const t = target.value
    if (!t || done.value) return
    const gen = generation
    try {
      const chunk = await pipelineApi.jobOutput(t.projectId, t.pipelineId, t.jobId, next)
      if (gen !== generation) return
      if (chunk.dropped) {
        if (next === 0) startsLate.value = true
        else skipped.value = true
      }
      if (chunk.data) {
        buffer.append(chunk.data)
        // A new array, so that what shows the lines sees them change.
        lines.value = buffer.lines.slice()
      }
      droppedLines.value = buffer.droppedLines
      notes.value = chunk.notes
      next = chunk.next
      done.value = chunk.done
      loaded.value = true
      error.value = ''
    } catch (e) {
      if (gen === generation) error.value = e instanceof Error ? e.message : 'Failed to read the output'
    }
  }

  const polling = computed(() => active.value && !done.value && target.value !== null)
  const { poll } = usePolling(read, POLL_INTERVAL_MS, polling)
  // The job asked for, as a key: the target is made afresh as the job's
  // record is read again, which must not start its output over.
  watch(
    () => (target.value ? `${target.value.projectId}:${target.value.pipelineId}:${target.value.jobId}` : ''),
    () => {
      reset()
      void poll()
    },
  )

  return { lines, notes, done, loaded, error, startsLate, skipped, droppedLines, refresh: poll }
}
