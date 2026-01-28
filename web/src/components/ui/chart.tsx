import * as React from "react"
import * as RechartsPrimitive from "recharts"

// Format: { THEME_NAME: CSS_SELECTOR }
const THEMES = { light: "", dark: ".dark" } as const

export type ChartConfig = {
  [k in string]: {
    label?: React.ReactNode
    icon?: React.ComponentType
    color?: string
    theme?: Record<keyof typeof THEMES, string>
  }
}

type ChartContextProps = {
  config: ChartConfig
}

const ChartContext = React.createContext<ChartContextProps | null>(null)

function useChart() {
  const context = React.useContext(ChartContext)

  if (!context) {
    throw new Error("useChart must be used within a <ChartContainer />")
  }

  return context
}

const ChartContainer = React.forwardRef<
  HTMLDivElement,
  React.ComponentProps<"div"> & {
    config: ChartConfig
    children: React.ComponentProps<
      typeof RechartsPrimitive.ResponsiveContainer
    >["children"]
  }
>(({ id, className, children, config, ...props }, ref) => {
  const uniqueId = React.useId()
  const chartId = `chart-${id || uniqueId.replace(/:/g, "")}`

  return (
    <ChartContext.Provider value={{ config }}>
      <div
        data-chart={chartId}
        ref={ref}
        className={className}
        style={{
          // Inject CSS variables for colors
          ...Object.entries(config).reduce((acc, [key, value]) => {
            if (value.color) {
              acc[`--color-${key}` as keyof typeof acc] = value.color
            }
            if (value.theme) {
              // For theme-based colors, use light theme as default
              acc[`--color-${key}` as keyof typeof acc] = value.theme.light
            }
            return acc
          }, {} as Record<string, string>),
          ...props.style,
        }}
        {...props}
      >
        <ChartStyle id={chartId} config={config} />
        <RechartsPrimitive.ResponsiveContainer>
          {children}
        </RechartsPrimitive.ResponsiveContainer>
      </div>
    </ChartContext.Provider>
  )
})
ChartContainer.displayName = "ChartContainer"

const ChartStyle = ({ id, config }: { id: string; config: ChartConfig }) => {
  const colorConfig = Object.entries(config).filter(
    ([, itemConfig]) => itemConfig.theme || itemConfig.color
  )

  if (!colorConfig.length) {
    return null
  }

  return (
    <style
      dangerouslySetInnerHTML={{
        __html: Object.entries(THEMES)
          .map(
            ([theme, prefix]) => `
${prefix} [data-chart=${id}] {
${colorConfig
  .map(([key, itemConfig]) => {
    const color =
      itemConfig.theme?.[theme as keyof typeof itemConfig.theme] ||
      itemConfig.color
    return color ? `  --color-${key}: ${color};` : null
  })
  .filter(Boolean)
  .join("\n")}
}
`
          )
          .join("\n"),
      }}
    />
  )
}

const ChartTooltip = RechartsPrimitive.Tooltip

// Tooltip payload item type
interface TooltipPayloadItem {
  name?: string
  dataKey?: string
  value?: number | string
  color?: string
  fill?: string
  payload?: Record<string, unknown>
}

interface ChartTooltipContentProps {
  active?: boolean
  payload?: TooltipPayloadItem[]
  label?: string
  hideLabel?: boolean
  hideIndicator?: boolean
  indicator?: "line" | "dot" | "dashed"
  nameKey?: string
  labelKey?: string
  formatter?: (value: number | string, name: string) => React.ReactNode
  className?: string
  color?: string
}

const ChartTooltipContent = React.forwardRef<
  HTMLDivElement,
  ChartTooltipContentProps
>(
  (
    {
      active,
      payload,
      className,
      indicator = "dot",
      hideLabel = false,
      hideIndicator = false,
      label,
      formatter,
      color,
      nameKey,
      labelKey,
    },
    ref
  ) => {
    const { config } = useChart()

    const tooltipLabel = React.useMemo(() => {
      if (hideLabel || !payload?.length) {
        return null
      }

      const [item] = payload
      const key = `${labelKey || item?.dataKey || item?.name || "value"}`
      const itemConfig = getPayloadConfigFromPayload(config, item, key)
      const value =
        !labelKey && typeof label === "string"
          ? config[label as keyof typeof config]?.label || label
          : itemConfig?.label

      if (!value) {
        return null
      }

      return <div style={{ fontWeight: 500 }}>{value}</div>
    }, [label, payload, hideLabel, labelKey, config])

    if (!active || !payload?.length) {
      return null
    }

    const nestLabel = payload.length === 1 && indicator !== "dot"

    return (
      <div
        ref={ref}
        className={className}
        style={{
          minWidth: '8rem',
          padding: '0.5rem 0.75rem',
          backgroundColor: 'var(--bg-primary, #ffffff)',
          border: '1px solid var(--border-color, #e5e7eb)',
          borderRadius: '0.5rem',
          boxShadow: '0 4px 6px -1px rgba(0, 0, 0, 0.1)',
        }}
      >
        {!nestLabel ? tooltipLabel : null}
        <div style={{ display: 'grid', gap: '0.375rem' }}>
          {payload.map((item: TooltipPayloadItem) => {
            const key = `${nameKey || item.name || item.dataKey || "value"}`
            const itemConfig = getPayloadConfigFromPayload(config, item, key)
            const indicatorColor = color || item.payload?.fill as string || item.color

            return (
              <div
                key={item.dataKey || item.name}
                style={{
                  display: 'flex',
                  width: '100%',
                  flexWrap: 'wrap',
                  alignItems: 'stretch',
                  gap: '0.5rem',
                  fontSize: '0.75rem',
                }}
              >
                {!hideIndicator && (
                  <div
                    style={{
                      flexShrink: 0,
                      borderRadius: indicator === "dot" ? "9999px" : "2px",
                      borderWidth: indicator === "dashed" ? "1.5px" : "0",
                      borderStyle: indicator === "dashed" ? "dashed" : "solid",
                      borderColor: indicatorColor,
                      backgroundColor: indicator === "dot" || indicator === "line" ? indicatorColor : "transparent",
                      width: indicator === "dot" ? "0.625rem" : "0.25rem",
                      height: indicator === "dot" ? "0.625rem" : "auto",
                      alignSelf: indicator === "dot" ? "center" : "stretch",
                    }}
                  />
                )}
                <div
                  style={{
                    display: 'flex',
                    flex: '1 1 0%',
                    justifyContent: 'space-between',
                    alignItems: 'baseline',
                    gap: '0.375rem',
                  }}
                >
                  <div style={{ display: 'grid', gap: '0.375rem' }}>
                    {nestLabel ? tooltipLabel : null}
                    <span style={{ color: 'var(--text-secondary, #6b7280)' }}>
                      {itemConfig?.label || item.name}
                    </span>
                  </div>
                  {item.value !== undefined && (
                    <span
                      style={{
                        fontFamily: 'ui-monospace, monospace',
                        fontWeight: 500,
                        color: 'var(--text-primary, #111827)',
                      }}
                    >
                      {formatter
                        ? formatter(item.value, item.name || '')
                        : typeof item.value === 'number'
                          ? item.value.toLocaleString()
                          : item.value}
                    </span>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      </div>
    )
  }
)
ChartTooltipContent.displayName = "ChartTooltipContent"

const ChartLegend = RechartsPrimitive.Legend

// Helper to extract item config from a payload.
function getPayloadConfigFromPayload(
  config: ChartConfig,
  payload: unknown,
  key: string
) {
  if (typeof payload !== "object" || payload === null) {
    return undefined
  }

  const payloadPayload =
    "payload" in payload &&
    typeof payload.payload === "object" &&
    payload.payload !== null
      ? payload.payload
      : undefined

  let configLabelKey: string = key

  if (
    key in payload &&
    typeof (payload as Record<string, unknown>)[key] === "string"
  ) {
    configLabelKey = (payload as Record<string, unknown>)[key] as string
  } else if (
    payloadPayload &&
    key in payloadPayload &&
    typeof (payloadPayload as Record<string, unknown>)[key] === "string"
  ) {
    configLabelKey = (payloadPayload as Record<string, unknown>)[key] as string
  }

  return configLabelKey in config
    ? config[configLabelKey]
    : config[key as keyof typeof config]
}

export {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  ChartLegend,
  ChartStyle,
  useChart,
}
