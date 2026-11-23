import { Marked, type Tokens } from 'marked'
import DOMPurify from 'dompurify'

// Where the app links into the documentation, from beside the things they
// explain. TestFrontendDocLinks (internal/docs) checks that every page and
// anchor named here exists, so keep each link a plain '/docs/...' string.
export const DOC_LINKS = {
  gettingStarted: '/docs/getting-started',
  projects: '/docs/projects',
  createProject: '/docs/projects#creating-a-project',
  standaloneWorkspaces: '/docs/projects#standalone-workspaces',
  environment: '/docs/projects#environment',
  setupScript: '/docs/projects#setup-script',
  upstream: '/docs/projects#upstream',
  previews: '/docs/projects#previews',
  protectedBranches: '/docs/branches#protected-branches',
  publishing: '/docs/branches#publishing',
  mergeRequests: '/docs/branches#merge-requests',
  reviews: '/docs/branches#reviews',
  autoMerge: '/docs/branches#automatic-merges',
  pipelines: '/docs/pipelines',
  pipelineSetting: '/docs/pipelines#project-setting',
  pipelineBranches: '/docs/pipelines#branches',
  pipelineJobs: '/docs/pipelines#how-jobs-run',
  pipelineStatuses: '/docs/pipelines#statuses',
  mergeRequestPipelines: '/docs/pipelines#merge-requests',
  mergeGate: '/docs/pipelines#merge-gate',
  pipelineFailures: '/docs/pipelines#failures',
  proposedPipeline: '/docs/pipelines#proposed',
  lookClosely: '/docs/pipelines#look-closely',
  tasks: '/docs/tasks',
  taskLinks: '/docs/tasks#linked-tasks',
  taskComments: '/docs/tasks#comments',
  assistant: '/docs/tasks#the-assistant',
  services: '/docs/services',
  projectServices: '/docs/services#project-services',
  aiKeys: '/docs/ai#provider-keys',
  aiModels: '/docs/ai#default-models',
  budgets: '/docs/ai#budgets',
  pricing: '/docs/ai#model-pricing',
  usage: '/docs/ai#usage',
  sshKey: '/docs/getting-started#ssh-key',
  gitIdentity: '/docs/getting-started#git-identity',
  administration: '/docs/administration',
} as const

export type DocTopic = keyof typeof DOC_LINKS

// Headings end with an explicit anchor, "## Protected branches
// {#protected-branches}", which becomes the heading's id.
const headingAnchor = /\s*\{#([a-z0-9-]+)\}\s*$/

const docsMarked = new Marked({ gfm: true })
docsMarked.use({
  renderer: {
    heading(this: { parser: { parseInline(tokens: Tokens.Generic[]): string } }, token: Tokens.Heading) {
      const html = this.parser.parseInline(token.tokens)
      const m = html.match(headingAnchor)
      if (!m) return `<h${token.depth}>${html}</h${token.depth}>\n`
      const text = html.slice(0, m.index)
      return `<h${token.depth} id="${m[1]}"><a class="doc-anchor" href="#${m[1]}" aria-hidden="true" tabindex="-1">#</a>${text}</h${token.depth}>\n`
    },
  },
})

// renderDoc renders a page's Markdown. The pages ship with devpad, but they
// go through the same sanitising as everything else rendered as HTML.
export function renderDoc(markdown: string): string {
  const raw = docsMarked.parse(markdown) as string
  return DOMPurify.sanitize(raw)
}
