interface Props {
  title: string
  items: { label: string; value: string | number }[]
}

export default function ReportHeader({ title, items }: Props) {
  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border mb-4">
      <h2 className="text-xl font-bold mb-3">{title}</h2>
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
        {items.map((item) => (
          <div key={item.label}>
            <div className="text-xs text-gray-500">{item.label}</div>
            <div className="font-semibold">{typeof item.value === 'number' ? item.value.toLocaleString() : item.value}</div>
          </div>
        ))}
      </div>
    </div>
  )
}
