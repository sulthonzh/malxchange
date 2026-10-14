// Content surface client (ticket #50): the read-only announcements and
// help endpoints the api proxies from admin-api (internal/api/content_CONTRACT.md).
// Every id is an opaque string, createTime is RFC3339 UTC, `content`
// fields are CMS-stored HTML that must pass utils/sanitize before v-html,
// and arrays are never null — an absent admin-api answers as empty
// content, never as an error, until #25 lands.
//
// Pagination follows the contract: pageNo starts at 1, pageSize is 1–50.

export interface ContentSummary {
  id: string
  title: string
  createTime: string
}

export interface ContentPage {
  content: ContentSummary[]
  totalElements: number
}

export interface AnnouncementDetail {
  info: ContentSummary & { content: string }
  back: ContentSummary | null
  next: ContentSummary | null
}

export interface HelpCategorySummary {
  cate: string
  title: string
  items: ContentSummary[]
}

export interface HelpArticle {
  id: string
  title: string
  createTime: string
  content: string
  cate: string
}

export function useContent() {
  const { apiFetch } = useApi()

  return {
    announcements(pageNo: number, pageSize: number): Promise<ContentPage> {
      return apiFetch<ContentPage>(
        `/announcements?${new URLSearchParams({
          pageNo: String(pageNo),
          pageSize: String(pageSize),
        })}`,
      )
    },

    announcement(id: string): Promise<AnnouncementDetail> {
      return apiFetch<AnnouncementDetail>(`/announcements/${encodeURIComponent(id)}`)
    },

    helpCategories(): Promise<HelpCategorySummary[]> {
      return apiFetch<HelpCategorySummary[]>('/help')
    },

    helpCategory(cate: string, pageNo: number, pageSize: number): Promise<ContentPage> {
      return apiFetch<ContentPage>(
        `/help/categories/${encodeURIComponent(cate)}?${new URLSearchParams({
          pageNo: String(pageNo),
          pageSize: String(pageSize),
        })}`,
      )
    },

    helpArticle(id: string): Promise<HelpArticle> {
      return apiFetch<HelpArticle>(`/help/articles/${encodeURIComponent(id)}`)
    },
  }
}
