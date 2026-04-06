import { useCallback, useRef } from 'react'
import html2canvas from 'html2canvas'
import jsPDF from 'jspdf'

interface Props {
  targetRef: React.RefObject<HTMLDivElement | null>
  filename?: string
}

export default function PDFExportButton({ targetRef, filename = 'benchmark-report' }: Props) {
  const exporting = useRef(false)

  const handleExport = useCallback(async () => {
    if (exporting.current || !targetRef.current) return
    exporting.current = true

    try {
      const canvas = await html2canvas(targetRef.current, {
        scale: 2,
        useCORS: true,
        logging: false,
      })

      const imgData = canvas.toDataURL('image/png')
      const pdf = new jsPDF('p', 'mm', 'a4')
      const pdfWidth = pdf.internal.pageSize.getWidth()
      const pdfHeight = pdf.internal.pageSize.getHeight()
      const imgWidth = pdfWidth - 20
      const imgHeight = (canvas.height * imgWidth) / canvas.width

      let yPos = 10
      let remainingHeight = imgHeight

      // First page
      pdf.addImage(imgData, 'PNG', 10, yPos, imgWidth, imgHeight)
      remainingHeight -= pdfHeight - 20

      // Additional pages if content overflows
      while (remainingHeight > 0) {
        pdf.addPage()
        yPos = yPos - pdfHeight + 10
        pdf.addImage(imgData, 'PNG', 10, yPos, imgWidth, imgHeight)
        remainingHeight -= pdfHeight - 10
      }

      pdf.save(`${filename}.pdf`)
    } finally {
      exporting.current = false
    }
  }, [targetRef, filename])

  return (
    <button
      onClick={handleExport}
      className="bg-gray-900 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-gray-700 transition-colors"
    >
      📄 Export PDF
    </button>
  )
}
