import MarkdownIt from 'markdown-it'
import { computed, type Ref } from 'vue'

const markdown = new MarkdownIt({ html: false, linkify: true, breaks: true })

/** Render Synaplan's markdown answers. HTML in the source is escaped. */
export function useRenderedMarkdown(source: Ref<string>) {
  return computed(() => (source.value ? markdown.render(source.value) : ''))
}
