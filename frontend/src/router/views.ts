import { lazyView } from './lazyView'

// The IDE is most of the app's code, so it is its own chunk, which the app
// prefetches once someone signs in.
export const ideView = lazyView(() => import('@/views/IdeView.vue'))
