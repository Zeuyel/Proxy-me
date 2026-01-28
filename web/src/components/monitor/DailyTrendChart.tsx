import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Bar, CartesianGrid, ComposedChart, Line, XAxis, YAxis } from 'recharts';
import type { UsageData } from '@/pages/MonitorPage';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/Card';
import {
  ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart';
import styles from '@/pages/MonitorPage.module.scss';

interface DailyTrendChartProps {
  data: UsageData | null;
  loading: boolean;
  isDark: boolean;
  timeRange: number;
}

interface DailyStat {
  date: string;
  requests: number;
  successRequests: number;
  failedRequests: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cachedTokens: number;
}

const formatTokensShort = (value: number) => {
  if (!Number.isFinite(value)) {
    return '0';
  }
  if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(1)}M`;
  }
  if (value >= 1_000) {
    const digits = value >= 10_000 ? 0 : 1;
    return `${(value / 1_000).toFixed(digits)}K`;
  }
  return value.toLocaleString();
};

export function DailyTrendChart({ data, loading, timeRange }: DailyTrendChartProps) {
  const { t } = useTranslation();

  // 按日期聚合数据
  const dailyData = useMemo((): DailyStat[] => {
    if (!data?.apis) return [];

    const dailyStats: Record<string, {
      requests: number;
      successRequests: number;
      failedRequests: number;
      inputTokens: number;
      outputTokens: number;
      reasoningTokens: number;
      cachedTokens: number;
    }> = {};

    // 辅助函数：获取本地日期字符串 YYYY-MM-DD
    const getLocalDateString = (timestamp: string): string => {
      const date = new Date(timestamp);
      const year = date.getFullYear();
      const month = String(date.getMonth() + 1).padStart(2, '0');
      const day = String(date.getDate()).padStart(2, '0');
      return `${year}-${month}-${day}`;
    };

    Object.values(data.apis).forEach((apiData) => {
      Object.values(apiData.models).forEach((modelData) => {
        modelData.details.forEach((detail) => {
          // 使用本地日期而非 UTC 日期
          const date = getLocalDateString(detail.timestamp);
          if (!dailyStats[date]) {
            dailyStats[date] = {
              requests: 0,
              successRequests: 0,
              failedRequests: 0,
              inputTokens: 0,
              outputTokens: 0,
              reasoningTokens: 0,
              cachedTokens: 0,
            };
          }
          dailyStats[date].requests++;
          if (detail.failed) {
            dailyStats[date].failedRequests++;
          } else {
            dailyStats[date].successRequests++;
            // 只统计成功请求的 Token
            dailyStats[date].inputTokens += detail.tokens.input_tokens || 0;
            dailyStats[date].outputTokens += detail.tokens.output_tokens || 0;
            dailyStats[date].reasoningTokens += detail.tokens.reasoning_tokens || 0;
            dailyStats[date].cachedTokens += detail.tokens.cached_tokens || 0;
          }
        });
      });
    });

    // 转换为数组并按日期排序
    return Object.entries(dailyStats)
      .map(([date, stats]) => ({ date, ...stats }))
      .sort((a, b) => a.date.localeCompare(b.date));
  }, [data]);

  // 准备图表数据
  const chartData = useMemo(() => {
    return dailyData.map((item) => {
      const date = new Date(item.date);
      const totalTokens = item.inputTokens + item.outputTokens + item.reasoningTokens;
      return {
        date: `${date.getMonth() + 1}/${date.getDate()}`,
        requests: item.requests,
        tokens: totalTokens,
      };
    });
  }, [dailyData]);

  // Calculate totals
  const totals = useMemo(() => ({
    requests: dailyData.reduce((acc, curr) => acc + curr.requests, 0),
    tokens: dailyData.reduce((acc, curr) => acc + curr.inputTokens + curr.outputTokens + curr.reasoningTokens, 0),
  }), [dailyData]);

  // 图表配置
  const chartConfig: ChartConfig = {
    requests: {
      label: t('monitor.trend.requests'),
      color: 'var(--chart-1)',
    },
    tokens: {
      label: t('monitor.trend.tokens'),
      color: 'var(--chart-2)',
    },
  };

  const timeRangeLabel = timeRange === 1
    ? t('monitor.today')
    : t('monitor.last_n_days', { n: timeRange });

  return (
    <Card className={styles.chartCard}>
      <CardHeader className={`${styles.chartHeader} flex flex-col items-stretch space-y-0 p-0 gap-0 border-b-2 border-b-border sm:flex-row`}>
        <div className="flex flex-1 flex-col justify-center gap-1 sm:py-0 py-4 px-6">
          <CardTitle className={styles.chartTitle}>{t('monitor.trend.title')}</CardTitle>
          <CardDescription className={styles.chartSubtitle}>
            {timeRangeLabel} · {t('monitor.trend.subtitle')}
          </CardDescription>
        </div>
        <div className="flex">
          {(['requests', 'tokens'] as const).map((key) => (
            <div
              key={key}
              data-active="true"
              className="data-[active=true]:bg-[var(--chart-1)] data-[active=true]:text-main-foreground text-foreground even:data-[active=true]:bg-[var(--chart-2)] relative z-10 flex flex-1 flex-col justify-center gap-1 px-6 py-4 text-left sm:border-t-0 border-t-border border-t-2 even:border-l-2 sm:border-l-2 border-l-border sm:px-8 sm:py-6"
            >
              <span className="text-xs">{chartConfig[key].label}</span>
              <span className="text-lg leading-none font-heading sm:text-3xl">
                {key === 'tokens'
                  ? formatTokensShort(totals.tokens)
                  : totals.requests.toLocaleString()}
              </span>
            </div>
          ))}
        </div>
      </CardHeader>

      <CardContent className={styles.chartContent}>
        {loading || dailyData.length === 0 ? (
          <div className={styles.chartEmpty}>
            {loading ? t('common.loading') : t('monitor.no_data')}
          </div>
        ) : (
          <ChartContainer
            config={chartConfig}
            className={`${styles.lineChartContainer} aspect-auto h-[250px] w-full`}
          >
            <ComposedChart
              accessibilityLayer
              data={chartData}
              margin={{
                top: 20,
                left: 12,
                right: 12,
              }}
            >
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey="date"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
                minTickGap={32}
              />
              <YAxis
                yAxisId="left"
                orientation="left"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
              />
              <YAxis
                yAxisId="right"
                orientation="right"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
                tickFormatter={(value) => formatTokensShort(Number(value))}
              />
              <ChartTooltip
                cursor={{ fill: "#8080804D" }}
                content={
                  <ChartTooltipContent
                    className="w-[150px]"
                    formatter={(value, name) => {
                      if (name === 'tokens') {
                        return formatTokensShort(Number(value));
                      }
                      return Number(value).toLocaleString();
                    }}
                  />
                }
              />
              <Bar
                dataKey="requests"
                yAxisId="left"
                fill="var(--color-requests)"
                radius={[4, 4, 0, 0]}
              />
              <Line
                dataKey="tokens"
                yAxisId="right"
                type="monotone"
                stroke="var(--color-tokens)"
                strokeWidth={2}
                dot={{
                  fill: "var(--color-tokens)",
                }}
                activeDot={{
                  r: 6,
                }}
              />
            </ComposedChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}
