import { ref, onUnmounted } from 'vue'

export type ResizeDirection = 'horizontal' | 'vertical'
export type ResizeEdge = 'left' | 'right' | 'top' | 'bottom'

interface UseResizableOptions {
  direction: ResizeDirection
  edge: ResizeEdge
  initialSize: number
  minSize?: number
  maxSize?: number
  onResize?: (size: number) => void
}

export function useResizable(options: UseResizableOptions) {
  const {
    direction,
    edge,
    initialSize,
    minSize = 100,
    maxSize = 2000,
    onResize,
  } = options

  const size = ref(initialSize)
  const isDragging = ref(false)

  let startPos = 0
  let startSize = 0

  function onPointerDown(e: PointerEvent) {
    e.preventDefault()
    isDragging.value = true
    startPos = direction === 'horizontal' ? e.clientX : e.clientY
    startSize = size.value

    document.addEventListener('pointermove', onPointerMove)
    document.addEventListener('pointerup', onPointerUp)
    document.body.style.cursor = direction === 'horizontal' ? 'col-resize' : 'row-resize'
    document.body.style.userSelect = 'none'
  }

  function onPointerMove(e: PointerEvent) {
    const currentPos = direction === 'horizontal' ? e.clientX : e.clientY
    const delta = currentPos - startPos

    let newSize: number
    if (edge === 'right' || edge === 'bottom') {
      // Dragging from right/bottom edge means moving pointer right/down shrinks the panel
      newSize = startSize - delta
    } else {
      // Dragging from left/top edge means moving pointer right/down grows the panel
      newSize = startSize + delta
    }

    newSize = Math.max(minSize, Math.min(maxSize, newSize))
    size.value = newSize
    onResize?.(newSize)
  }

  function onPointerUp() {
    isDragging.value = false
    document.removeEventListener('pointermove', onPointerMove)
    document.removeEventListener('pointerup', onPointerUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  onUnmounted(() => {
    document.removeEventListener('pointermove', onPointerMove)
    document.removeEventListener('pointerup', onPointerUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  })

  return {
    size,
    isDragging,
    onPointerDown,
  }
}
